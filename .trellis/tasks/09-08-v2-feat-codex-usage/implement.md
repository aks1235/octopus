# 实施计划:渠道 Key 运营包

前置:prd/design 经用户审阅;`task.py start` 后才动代码。

## 步骤(每步一 commit,可独立回滚)

1. [x] R1 后端:key 统计读取端点 + op 读取(handlers 现有风格)
   - 验证:容器内 go build + go test;curl 端点对演练库多 key 渠道返回全量统计
   - ✅ 2026-09-09:GET /channel/key-stats/:id;op 读 channelKeyCache;单测 4 用例;容器内编译+测试绿
2. [x] R1 前端:Stats.tsx(退路 Card.tsx)凭据维度展示
   - 验证:前端 lint + build;本地容器人看
   - ✅ 2026-09-09:挂点选 Stats.tsx(渠道统计弹窗本位,KeyStatsPanel 与模型行同形五列);三语 locale;lint+build 绿;容器人看并入步骤 7
3. [x] R2 后端:test-key 端点——先验证 buildOutbound 合成 grant 可行性(设计退路见 design.md D2),再实现
   - 验证:go test;curl 对可用渠道成功、坏 base_url/坏 key 失败带摘要
   - ✅ 2026-09-09:走主路(buildOutbound+合成 ChannelGrant{Protocols:全位},零改 relay 既有文件);relay/keytest.go+6 用例绿
4. [x] R2 前端:FormKeys 凭据行测试按钮 + 逐模型结果面板
   - 验证:lint + build;人看
   - ✅ 2026-09-09:凭据行 Zap 按钮+逐模型面板(可单模型重测);三语 locale;lint+build 绿
5. [x] R3 Switch 白拿验证:容器实测 + 记录入 research/
   - ✅ 2026-09-10:用户 UI 确认(禁用 key 统计保留+徽标),留档 research/verify-ui-2026-09-10.md
6. [x] R4 文档:ADR-0002 第三次勘误;父任务 prd 任务地图第 6 项+AC 措辞;research/ 复活决策备注
   - ✅ 2026-09-09(主会话完成):勘误 8 项+理由;父地图/AC 已改;research/revival-decisions.md 落盘
7. [x] octopus-verify 全流程闭环(skill 硬关卡,写 marker;用 data-verify 隔离目录,勿动 ./data)
   - ✅ 2026-09-10:smoke 8081 闭环(两轮 UI 反馈均修复复验);marker 已写,提交后对齐 HEAD;过程留档 research/verify-ui-2026-09-10.md

## 回滚点

- 每步独立 revert;无 schema/迁移变更,无数据善后
- 步骤 3 若两条路径(buildOutbound 复用/协议位循环)都遇阻,回滚该步并带事实回规划(不硬写第三种)

## 验证命令备忘(以 octopus-verify skill 流程为准,此处仅备忘)

- 后端:容器内 `go build -buildvcs=false ./...` + `go test ./internal/...`
- 前端:web/ 下 lint + build
- 端点冒烟:本地容器 curl(管理 token)
