# 冒烟矩阵与停机切换 v2.0.0 — 执行清单

## Phase A: 冒烟矩阵

### A1: 上游白拿项冒烟
- [ ] A1.1 起本地容器(`docker compose up`,用 `data-verify/` 隔离)
- [ ] A1.2 多 Key 轮询:配置 2+ Key,发 3+ 请求,日志验证 Key 交替
- [ ] A1.3 failover:主渠道用无效 Key,验证切到备渠道
- [ ] A1.4 gzip:curl 带 `Accept-Encoding: gzip`,验证响应头
- [ ] A1.5 SSE 实时流:日志页实时推送,流正常结束

### A2: axonhub/llm 行为等价性冒烟
- [ ] A2.1 DeepSeek thinking 非透传:reasoning_content 正确返回
- [ ] A2.2 DeepSeek thinking 透传回传:reasoning_content 无丢失
- [ ] A2.3 工具调用 reasoning_content:thinking + tool_call 共存
- [ ] A2.4 空 thinking:短 prompt 不出错
- [ ] A2.5 不合成 signature:代码审查 + 调用验证
- [ ] A2.6 Anthropic reasoning 参数透传
- [ ] A2.7 content_block_start 保留
- [ ] A2.8 thinking block 修补
- [ ] A2.9 缓存前缀不被透传复写破坏
- [ ] A2.10 json_schema Chat/Responses API 一致性 + strict 往返
- [ ] A2.11 跨渠道重试适配器状态隔离
- [ ] A2.12 2xx 空响应触发重试
- [ ] A2.13 Responses API failed/error 触发重试
- [ ] A2.14 SSE [DONE] 正常结束

### A3: 功能包冒烟
- [ ] A3.1 ops-obs:健康检查自动运行
- [ ] A3.2 ops-obs:渠道禁用/解禁
- [ ] A3.3 ops-obs:分组正则成员
- [ ] A3.4 log-persistence:日志落库+历史查询+attempts
- [ ] A3.5 client-theme:客户端识别+主题+思考等级
- [ ] A3.6 codex-usage:Key 统计读侧
- [ ] A3.7 codex-usage:单 Key 测试

### A4: 迁移脚本最终验证
- [ ] A4.1 复制最新 data/ 到 data-verify/
- [ ] A4.2 跑迁移脚本,对账(113 渠道 + 1284 日志 + 用户/设置)
- [ ] A4.3 用转换后 v2.db 起容器,确认无 migration 错误

## Phase B: 发版准备

- [ ] B1 确认版本号 v2.0.0(CI ldflags 注入链路正确)
- [ ] B2 octopus-verify 全流程(容器内编译→后端测试→前端 lint+build→起容器→健康检查)
- [ ] B3 octopus-publish:打 tag v2.0.0 → 推 Docker Hub → 更新 compose → 推 GitHub → Release

## Phase C: 生产切换

- [ ] C1 停 v1.0.4 + 备份 data/
- [ ] C2 迁移脚本跑正式库
- [ ] C3 起 v2 容器 + 验证
- [ ] C4 回滚预案确认(镜像钉 v1.0.4 + 数据备份)
- [ ] C5 更新父任务地图,标记第 7 项完成

## Review Gates

- **A-gate**: Phase A 全项通过后才能进入 Phase B
- **B-gate**: octopus-verify marker 存在后才能执行 octopus-publish
- **C-gate**: 用户确认切换窗口后才能执行生产切换

## Rollback Points

- Phase A 失败 → 修复不等价项,重跑冒烟
- Phase B 失败 → 修 CI/构建问题,重跑 verify
- Phase C 失败 → 回滚到 v1.0.4 + 数据备份
