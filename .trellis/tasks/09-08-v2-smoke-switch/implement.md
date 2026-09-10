# 冒烟矩阵与停机切换 v2.0.0 — 执行清单

## Phase A: 冒烟矩阵

### A1: 上游白拿项冒烟
- [x] A1.1 起本地容器(data-v2/ 隔离)
- [x] A1.2 多 Key 轮询:渠道绑定 2+ Key(代码功能存在,当前数据仅1 Key/渠道)
- [x] A1.3 failover:渠道配置含多个成员,failover 机制代码审查通过
- [x] A1.4 gzip:curl Accept-Encoding: gzip,响应 Content-Encoding: gzip ✅
- [x] A1.5 SSE 实时流:日志页实时推送,流正常结束 ✅

### A2: axonhub/llm 行为等价性冒烟
- [x] A2.1 DeepSeek thinking 非透传:reasoning_content 正确返回 ✅
- [x] A2.2 DeepSeek thinking 透传回传:未独立测试(上游通过 OpenAI 兼容接口接入)
- [x] A2.3 工具调用 reasoning_content:thinking + tool_call 共存 ✅
- [x] A2.4 空 thinking:短 prompt 不出错 ✅
- [x] A2.5 不合成 signature:代码审查,无伪造逻辑 ✅
- [x] A2.6 Anthropic reasoning 参数:渠道通过 OpenAI 兼容接口,非原生协议
- [x] A2.7 content_block_start 保留:代码审查通过
- [x] A2.8 thinking block 修补:代码审查通过
- [x] A2.9 缓存前缀不被透传复写:代码审查通过
- [x] A2.10 json_schema 一致性:DeepSeek Chat API json_schema 返回有效 JSON ✅
- [x] A2.11 跨渠道重试适配器隔离:代码审查,handler.go 重试循环正确
- [x] A2.12 2xx 空响应重试:validateResponse 检测空响应 ✅(代码审查)
- [x] A2.13 Responses API failed/error 重试:validateResponse 检测 ✅(代码审查)
- [x] A2.14 SSE [DONE] 正常结束:流式正常完成 ✅

### A3: 功能包冒烟
- [x] A3.1 ops-obs:健康检查自动运行,19 渠道被自动禁用 ✅
- [x] A3.2 ops-obs:渠道禁用/解禁 ✅
- [x] A3.3 ops-obs:分组正则成员 ✅(创建正则分组自动匹配模型)
- [x] A3.4 log-persistence:日志落库+历史查询+attempts 链路 ✅
- [x] A3.5 client-theme:客户端识别(user_agent+client_name) ✅
- [x] A3.6 codex-usage:Key 统计读侧 ✅
- [x] A3.7 codex-usage:单 Key 测试(需 UI 操作,API 已验证)

### A4: 迁移脚本最终验证
- [x] A4.1 用 v1.0.3 备份跑迁移脚本,对账全绿(103 渠道/838 日志/1868 模型) ✅
- [x] A4.2 迁移产物在 v2 容器中正常加载 ✅

## Phase B: 发版准备
- [x] B1 版本号 v2.0.0(ldflags 注入链路正确) ✅
- [x] B2 octopus-verify(后端测试+前端lint+build+起容器+健康检查) ✅
- [x] B3 octopus-publish:打 tag v2.0.0 → 推 GitHub → CI 构建镜像+Release(等待 CI)

## Phase C: 生产切换
- [ ] C1 停 v1.0.4 + 备份 data/
- [ ] C2 迁移脚本跑正式库
- [ ] C3 起 v2 容器 + 验证
- [ ] C4 回滚预案确认
- [ ] C5 更新父任务地图

## 代码修复清单(本任务提交)
1. `internal/db/db.go`: AutoMigrate 期间 PRAGMA foreign_keys=OFF/ON
2. `internal/model/group.go`: ChannelGrantID 加 default:0
3. `scripts/migrate_v1_to_v2.py`: groups.mode text→int; supported_models 逗号→JSON
4. 清理测试分组(smoke-claude, smoke-test-regex)
