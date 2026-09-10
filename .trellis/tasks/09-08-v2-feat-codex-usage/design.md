# 设计:渠道 Key 运营包

## 总体

两个独立小件,零表结构变更、零迁移影响。推迟项(Codex OAuth 渠道/Usage Card/auth 导入)的设计输入全部在 research/:upstream-v2-reality.md §6(axonhub/llm 库 codex+oauth 能力)、fork-implementation.md 子项 1/2/3(表列名与机制)、migration-impact.md(复活时的迁移联动)。本文档只覆盖收缩后的范围。

## D1 Key 级统计读侧(R1)

- **后端**:handlers 新增 key 统计读取端点(命名随 stats.go/channel.go 现有风格,形如 GET /api/v1/channel/:id/key-stats);op 层读 ChannelKey(ID/Name/Enabled + 内嵌 StatsMetrics),gorm 现有查询即可,无新 SQL 形状;响应 `{keys:[{id,name,enabled,stats{...}}]}`
- **口径**:直接吐 StatsMetrics 原始七列,与模型统计同 freshness(定时落库);不做聚合/趋势(被排除的统计增强包不借尸还魂)
- **前端挂点**:优先 channel/Stats.tsx(已有统计语义与排序交互,389 行,加一个凭据维度);若实现期发现改动超预期,退路是 Card.tsx 展开 key 行——数据端点不变,纯 UI 取舍
- 不加缓存:读库即可,量级(百 key)无压力

## D2 单 Key 连通性测试(R2,2026-09-10 修订)

- **后端**:POST /api/v1/channel/test-key `{channelId 或表单配置, key, key_name, models[]}`
  - 复用 relay 的 buildOutbound(internal/relay/channel.go:24)构出站请求——「测的真链路」的关键,不另写 HTTP 客户端
  - **协议位精确化**:按「模型×key_name」从请求携带 grants 查实际授权位构临时 grant;查不到/位 0 回退全协议位(一轮实测后的修订:全协议位无差别尝试对不允许测活的站请求量放大 3 倍)
  - 每模型:最小非流式 payload(max_tokens=1 量级)、30s 超时;2xx 且可解析即成功;错误截断 200 字符
  - **日志写入**:每被测模型一条 RelayLog(仿 relayLogFinalize 组装),ClientName=「面板测试」、UserAgent="octopus-key-test"、RequestAPIKeyName=key_name、tokens/cost=0、UseTime 实测、Error 填失败摘要;未保存表单渠道 ChannelId=0;零表结构变更
- **前端**:FormKeys.tsx 凭据行 ⚡ 默认**单模型测活**(该凭据首个授权模型,1 个请求);第二入口「测全部模型」;结果面板逐模型列出、可单模型重测;无对话框
- **并发**:逐模型串行(测试由人工触发且计费,串行控压)

## D3 Switch 白拿验证(R3)

容器内禁用某 key → 转发请求确认不再命中(ChannelAttempt 带 key 名可观测)→ 重新启用恢复;统计值保留。记录+截图入 research/。

## 兼容与回滚

- R1/R2 各自独立 commit;回滚 = revert 单 commit,无 schema 变更无善后
- 推迟项零代码:AutoMigrate 不注册 usage_cards/o_auth_sessions
- 存量行为零变化:只加读端点与新按钮,不动选路/转发链路

## 风险

| 风险 | 缓解 |
|---|---|
| buildOutbound 与 grant 类型强耦合 | 退路:handler 内按协议位循环构请求(见 D2);实现期第一步先验证签名 |
| Stats.tsx 挂点改动超预期 | 退路:Card.tsx 展开行,端点不变 |
| 测试请求产生真实计费消耗 | 用 max_tokens=1 量级+用户手动触发(无定时),文档注明 |
