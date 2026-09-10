package relay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

// keyTestTimeout 单次测试请求的上游等待上限: 测的是连通性, 30 秒拿不到可解析响应即按失败计。
const keyTestTimeout = 30 * time.Second

// keyTestMaxTokens 单次测试请求的生成上限, 控制真实请求的计费消耗在最小量级。
const keyTestMaxTokens int64 = 1

// keyTestMaxErrorLength 失败原因的截断上限, 与 probe 包同值: 部分上游报错是整页 HTML, 全文带进结果无用。
const keyTestMaxErrorLength = 200

// keyTestClientName / keyTestUserAgent 测试请求在转发日志里的固定标识:
// 与 claude-code 等客户端标识同字段同位置, 日志页由此一眼区分人工测试与真实转发。
const keyTestClientName = "面板测试"

const keyTestUserAgent = "octopus-key-test"

// keyTestProtocols 按序尝试的出站协议位, 顺序与 buildOutbound 非透传时的选择一致(Anthropic 优先)。
// 渠道各协议端点是否真实可用只有发请求才知道, 任一协议成功即判连通, 全部失败才汇总报错。
var keyTestProtocols = []model.Protocol{
	model.ProtocolAnthropicMessage,
	model.ProtocolOpenAIResponse,
	model.ProtocolOpenAIChatCompletion,
}

// TestChannelKey 按渠道配置与凭据对每个模型发起最小真实请求, 返回逐模型的连通性结果并逐模型落一条测试日志。
// 请求经 buildOutbound 构造出站转换器, 与转发链路共用同一套地址拼接与凭据注入, 由此测到的可用性
// 即真实转发时的可用性。协议位优先取授权里"模型 x 凭据"的实际位: 有授权时只试授权协议,
// 查不到或位里没有已知协议才回退全协议位 —— 合成的临时授权不查库也不落库, 与凭据探测
// "两侧都试、任一成功即健康"的宽容判定同一思路。
// 模型之间串行执行: 测试由人工触发且产生真实计费消耗, 串行既控制压力也保证结果顺序与提交一致。
func TestChannelKey(ctx context.Context, channel model.Channel, key, keyName string, models []string, grants []model.ChannelGrantConfig) []model.ChannelKeyTestResult {
	channelKey := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Name: keyName, Key: key}}
	results := make([]model.ChannelKeyTestResult, 0, len(models))
	for _, modelName := range models {
		if ctx.Err() != nil {
			// 客户端已断开, 剩余模型不再发往上游, 按取消摘要逐个记失败, 日志与结果行一一对应。
			result := model.ChannelKeyTestResult{ModelName: modelName, Error: ctx.Err().Error()}
			results = append(results, result)
			keyTestLogFinalize(channel, channelKey, result, nil, 0)
			continue
		}
		protocols := keyTestModelProtocols(grants, modelName, keyName)
		startedAt := time.Now()
		result, attempts := testChannelKeyModel(ctx, channel, channelKey, modelName, protocols)
		results = append(results, result)
		keyTestLogFinalize(channel, channelKey, result, attempts, time.Since(startedAt).Milliseconds())
	}
	return results
}

// keyTestModelProtocols 定出单个模型的试测协议序列: 取授权里"模型 x 凭据"的实际协议位
// (同一组合出现多条时取并集)并按尝试优先级展开; 查不到授权或位里没有已知协议时回退全协议位,
// 不因授权缺失或异常而漏测。
func keyTestModelProtocols(grants []model.ChannelGrantConfig, modelName, keyName string) []model.Protocol {
	var mask model.Protocol
	for _, grant := range grants {
		if grant.ModelName == modelName && grant.KeyName == keyName {
			mask |= grant.Protocols
		}
	}
	protocols := make([]model.Protocol, 0, len(keyTestProtocols))
	for _, protocol := range keyTestProtocols {
		if mask&protocol != 0 {
			protocols = append(protocols, protocol)
		}
	}
	if len(protocols) == 0 {
		return keyTestProtocols
	}
	return protocols
}

// testChannelKeyModel 对单个模型按给定协议逐个试测: 任一协议成功即成功并记录该协议,
// 全部失败时聚合各协议的错误摘要并截断, 界面上由此可直接看到每侧的原因。
// 每次真实发往上游的请求记一条 ChannelAttempt, 供测试日志回放"试了哪些协议端点"。
func testChannelKeyModel(ctx context.Context, channel model.Channel, channelKey model.ChannelKey, modelName string, protocols []model.Protocol) (model.ChannelKeyTestResult, []model.ChannelAttempt) {
	result := model.ChannelKeyTestResult{ModelName: modelName}
	failures := make([]string, 0, len(protocols))
	attempts := make([]model.ChannelAttempt, 0, len(protocols))
	for _, protocol := range protocols {
		// 单协议位的临时授权使 buildOutbound 恒命中透传分支, 返回的即本协议的出站转换器。
		grant := model.ChannelGrant{Protocols: protocol}
		outbound, _, _, err := buildOutbound(channel, grant, channelKey, protocol)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		attemptStartedAt := time.Now()
		err = sendKeyTestRequest(ctx, channel, outbound, modelName)
		attempt := model.ChannelAttempt{
			ChannelID:        channel.ID,
			ChannelName:      channel.Name,
			ChannelKeyRemark: keyTestProtocolName(protocol),
			ModelName:        modelName,
			AttemptNum:       len(attempts) + 1,
			Duration:         int(time.Since(attemptStartedAt).Milliseconds()),
		}
		if err != nil {
			attempt.Status = model.AttemptFailed
			attempt.Msg = truncateKeyTestError(err.Error())
			attempts = append(attempts, attempt)
			failures = append(failures, err.Error())
			continue
		}
		attempt.Status = model.AttemptSuccess
		attempts = append(attempts, attempt)
		result.Success = true
		result.Protocol = protocol
		return result, attempts
	}
	result.Error = truncateKeyTestError(strings.Join(failures, "; "))
	return result, attempts
}

// keyTestProtocolName 协议位在尝试备注里的可读名称, 供日志页区分同一模型试过的各个协议端点。
func keyTestProtocolName(protocol model.Protocol) string {
	switch protocol {
	case model.ProtocolAnthropicMessage:
		return "anthropic message"
	case model.ProtocolOpenAIResponse:
		return "openai responses"
	case model.ProtocolOpenAIChatCompletion:
		return "openai chat"
	default:
		return fmt.Sprintf("protocol %d", protocol)
	}
}

// keyTestLogFinalize 把单个模型的测试结果落成一条转发日志, 组装方式与 relayLogFinalize 同构。
// 每模型一条, ID 走 RelayLogAdd 的同一雪花路径, 时间为秒级; tokens/cost 恒为 0:
// 最小请求的用量不参与渠道统计对账, 日志只用于回放"何时测的、测了哪些协议、通没通"。
// 渠道可能是尚未保存的表单渠道: 此时渠道主键取 0, 名称用表单名, 日志照写。
func keyTestLogFinalize(channel model.Channel, channelKey model.ChannelKey, result model.ChannelKeyTestResult, attempts []model.ChannelAttempt, useTime int64) {
	relayLog := model.RelayLog{
		Time:              time.Now().Unix(),
		RequestModelName:  result.ModelName,
		ActualModelName:   result.ModelName,
		RequestAPIKeyName: channelKey.Name,
		ChannelId:         channel.ID,
		ChannelName:       channel.Name,
		UseTime:           int(useTime),
		Error:             result.Error,
		UserAgent:         keyTestUserAgent,
		ClientName:        keyTestClientName,
		Attempts:          attempts,
		TotalAttempts:     len(attempts),
	}
	if err := op.RelayLogAdd(context.Background(), relayLog); err != nil {
		log.Warnf("failed to save key test log: %v", err)
	}
}

// sendKeyTestRequest 按指定协议构最小非流式请求并发往上游, 2xx 且响应可按该协议解析才算连通。
// 请求体经出站转换器生成(与转发链路同构), 渠道的参数覆盖与自定义 Header 同样生效;
// 代理选择与转发同源, 由此测到的路径才是真实转发会走的路径。
func sendKeyTestRequest(ctx context.Context, channel model.Channel, outbound transformer.Outbound, modelName string) error {
	probeText := "probe"
	maxTokens := keyTestMaxTokens
	request, err := outbound.TransformRequest(ctx, &llm.Request{
		Model:     modelName,
		Messages:  []llm.Message{{Role: "user", Content: llm.MessageContent{Content: &probeText}}},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		return err
	}
	if err := applyChannelConfig(channel, request); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, keyTestTimeout)
	defer cancel()

	httpClient, closeIdle, err := resolveUpstreamClient(channel)
	if err != nil {
		return err
	}
	if closeIdle != nil {
		defer closeIdle()
	}

	response, err := httpclient.NewHttpClientWithClient(httpClient).Do(ctx, request)
	if err != nil {
		var failure *httpclient.Error
		if errors.As(err, &failure) {
			// 上游错误原文最有诊断价值, 状态行带正文一并返回, 汇总层统一截断。
			return fmt.Errorf("upstream %s: %s", failure.Status, strings.TrimSpace(string(failure.Body)))
		}
		return err
	}
	// 2xx 之后仍解析一次: 确认响应是所选协议讲得通的形状, 空壳 200 不算连通。
	if _, err := outbound.TransformResponse(ctx, response); err != nil {
		return fmt.Errorf("unparsable response: %v: %s", err, strings.TrimSpace(string(response.Body)))
	}
	return nil
}

// truncateKeyTestError 把失败原因截断到上限以内, 与 probe 包的截断风格一致。
func truncateKeyTestError(message string) string {
	if len(message) <= keyTestMaxErrorLength {
		return message
	}
	return message[:keyTestMaxErrorLength]
}
