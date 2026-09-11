# 技术设计:升级 axonhub/llm 至 0909 快照

## 升级方式

容器内操作(无本机 go):

```bash
docker run --rm -v "$PWD":/src -w /src -v octopus-go-mod:/go/pkg/mod -v octopus-go-build-cache:/root/.cache/go-build golang:1.26 \
  sh -c "go get github.com/looplj/axonhub/llm@3786f2c5c8de golang.org/x/crypto@v0.55.0 golang.org/x/net@v0.58.0 && go mod tidy && go build -tags=jsoniter ./... && echo OK"
```

- `@3786f2c5c8de` 直接指 commit,落盘即伪版本 `v0.0.0-20260909170523-3786f2c5c8de`,与上游 1bd2ed8 的 go.mod 对得上(以其为校验基准:`git show 1bd2ed8:go.mod`)。
- x/crypto、x/net 一并升(1bd2ed8 同款版本);若 tidy 拉入其他间接依赖变动,逐项对照上游 go.mod,超出范围的如实上报再定。
- static/out 已有产物满足 embed;构建验证前勿删。

## 冒烟设计:两层

### 层 1:模拟器自动化(主判定)

扩展 09-11-port 任务的 sim 思路,在宿主起多形状假上游(端口区分),容器经 compose 网关回连:

| 模拟端点 | 形状 | 断言点 |
|---|---|---|
| `/v1/models` + `/v1/chat/completions`(OpenAI 形,含 `reasoning_content` 字段流) | GLM/DeepSeek 风格 | 思考内容(常开 thinking/reasoning_content)在 OpenAI 出站请求中正确携带;工具调用 tool_calls 往返保留 |
| `/v1/messages`(Anthropic 形) | Claude 客户端入口 | cache_control 断点透传到出站;system 多条合并不丢内容 |
| SSE 流 | 分片 + [DONE] + 中途断连 | 终态(success/failed)不因断连丢失;[DONE] 后无多余帧 |

自动化驱动脚本复用既有姿势(admin cookie + curl + python 断言),逐项输出 PASS/FAIL。
模拟器落盘 `scripts/e2e/`(不再放 /tmp,形成可复跑资产)。

### 层 2:真渠道抽查(8081 指真上游)

- GLM-5.3(必测,用户有渠道):发一条对话,验证常开思考在客户端可见、日志页思考等级/时长正常。
- DeepSeek 工具调用一条、Claude 客户端(OpenAI 兼容路径)一条。
- 用户配合发请求或提供可用渠道凭据;不可自动化时列清单请用户点验。

### 失败处理

- 转换回归 → 先看 llm 差异点是否可配置规避;不行则如实评估:回退依赖(git checkout go.mod/go.sum)或接受差异,用户决策。
- 基线项失败但定向项通过 → 大概率是上游有意的行为修正,逐项对比 llm 提交说明定性。

## 风险

- 伪版本直接钉 commit,与上游完全一致,无浮动。
- go mod tidy 可能触碰较多间接条目:对照 1bd2ed8 的 go.sum 差异,超范围上报。
- 新版库若改了 Octopus 用到的 API 签名(编译错),按编译错逐个适配——25 提交里未见 breaking 迹子(上游 octopus 同步升级无适配提交),预期为零。

## 回滚

`git checkout HEAD -- go.mod go.sum` 即回滚;发布前无任何 schema/代码改动(纯依赖 + 可能的小适配)。
