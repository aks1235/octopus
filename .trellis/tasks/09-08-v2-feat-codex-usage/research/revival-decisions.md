# 复活预选决策(推迟项增量补回时的设计输入)

- **Query**: Codex OAuth 渠道/Usage Card/auth 导入推迟后,复活时已预选的方案与必须先验证的硬前提
- **Scope**: internal(2026-09-09 规划期与用户对齐,未实施)
- **Date**: 2026-09-09

## 与用户预选的方案(当时拍板,复活时默认沿用、可重议)

1. **Codex 授权模式:设备码流**(axonhub/llm `oauth.DeviceFlowProvider`)——面板生成设备码→用户在 auth.openai.com 输入→后端轮询拿 token;无需公网回调、无需 o_auth_sessions 表。备选:复刻 fork 网页回调流(需回调端点+按 fork 列名建 o_auth_sessions,数据预期空)。
2. **Key 轮询:分组可选路由策略**(sticky 默认/round-robin opt-in,动 route.go pickGroupItem)——仅当出现真实多账号/摊配额需求时实施;2026-09-09 用户确认无此需求,不做。

## 复活时的硬前提验证(当时识别,未验证)

1. **axonhub/llm 版本差**:go.mod 钉 v0.0.0-20260901162339,codex/oauth 包 API 面按本地缓存 0810 版核对——先容器内 `go doc` 验证 0901 签名再定设计(upstream-v2-reality.md §6 末注)。
2. **存量 Codex 凭证 JSON 兼容**:fork `CodexCredential.expires_at` 是 RFC3339 字符串、库 `OAuthCredentials` 期望 `time.Time`——解析不过则迁移带入的 key 全不可用(migration-impact.md §3)。
3. **表列名约束**:若建 usage_cards,列名必须与 fork 完全一致(migration-impact.md §1 清单),迁移脚本零改动自动携带。
4. **加密行为**:fork `SetEncryptionKey` 从未被调用,存量实为明文——v2 若实现真加密需启动期兼容判断,或保持「无密钥=明文」同行为。

## 相关研究文件

- fork-implementation.md — 三件套的 fork 侧完整机制(表列/包/端点/前端)
- upstream-v2-reality.md §6 — axonhub/llm 库 codex transformer+oauth 全套能力
- migration-impact.md — 迁移联动与风险表
- scope-triage.md — 本次收缩的逐项判定依据
