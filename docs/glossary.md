# 术语表

| 术语 | 含义 |
|---|---|
| **dev** | fork 生产线分支,基点上游 `9851442`(2026-04-09),含全部自有补丁;2026-09-08 起冻结,仅留 v1.0.x 热修 |
| **dev-v2** | 重做分支,基线 `upstream/master@27aa40d`(v0.13.2),首发 v2.0.0 |
| **feat/my-features** | 2026-08 已废弃的路线 A 迁移分支,保留仅作移植对照 |
| **路线 A** | 「以新上游为基重做 fork 功能」的迁移路线代称(区别于继续在旧基线上 cherry-pick) |
| **upstream / origin / aks-fork** | 三个 remote:`bestruirui/octopus`(最上游)/ `chicring/octopus`(clone 源)/ `aks1235/octopus`(用户自有仓库,CI 推送目标) |
| **chenjh** | fork 侧主要贡献者身份(139 个 commit:Usage Card、Codex OAuth、UA 识别等),在上游 0 commit |
| **A2 脚本** | feat 分支上的渠道导出/导入脚本(`export_channels.py`/`import_channels.py`),多URL/多KEY 降级导出,103/103 验证 |
| **迁移链** | `internal/db/migrate/003-012.go`,fork 与上游各自演化已分叉,互不兼容 |
| **axonhub/llm** | 上游 v0.11+ 的协议转换依赖库,取代了 fork 侧旧 internal/transformer 层 |
| **Grants** | 上游 v0.12+ 渠道模型中「模型 ↔ 凭据」的授权关联结构 |
| **冒烟矩阵** | ADR-0007 ④:对 axonhub/llm 行为等价性的逐项验证清单(DeepSeek thinking 族、Anthropic 透传族等) |
| **日志持久化** | fork 独有能力:relay_logs 入库+筛选+详情;上游仅内存 SSE 流,migrate/009 会删表 |
| **Usage Card** | fork 独有:Coding Plan 配额监控卡片(数据表 `usage_cards`) |
| **octopus-verify / octopus-publish** | 本地验证 / 发布斜杠命令,`.octopus-verified` marker 为发版硬关卡 |
| **停机窗口** | ADR-0005 的切换方式:停 v1.0.4 → 备份 → 脚本转换 → 起 v2 验证,不做双库并行 |
