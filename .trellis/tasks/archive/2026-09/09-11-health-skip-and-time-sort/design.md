# 技术设计

## R1: 健康检查跳过开关

### 数据模型

`internal/model/channel.go` — `ChannelConfig` 加字段(用户可编辑配置,随编辑形态读写自动带出,AutoMigrate 自动加列,存量默认 false):

```go
HealthCheckSkip bool `json:"health_check_skip" gorm:"default:false"` // 跳过健康检查; 适用于无模型列表接口的渠道(探测必 404 但转发可用)。
```

放在 `MatchRegex` 之后。不放健康状态列区块(那些由任务维护、不出 JSON)。

### 后端逻辑

1. **候选过滤** `internal/op/channel.go:371` `ChannelHealthCandidates`:
   循环内加 `if channel.HealthCheckSkip { continue }`——跳过的渠道不探测、不计数、不禁用、不解禁。

2. **勾选即恢复** `ChannelUpdate`(保存渠道处):
   保存时若 `HealthCheckSkip == true` 且渠道当前 `AutoDisabled`,则置 `Enabled=true, AutoDisabled=false, HealthFailCount=0`。这样被误禁用的渠道勾选开关保存后立即恢复(AC3),无需等健康检查轮次。参考现有 `reenable` 逻辑(`channel.go:360` 附近的写法)。

### 前端

- `web/src/components/modules/channel/Form.tsx`: 表单加 Switch/Checkbox,放"代理"或"匹配正则"附近的配置区,带说明文案「无模型列表接口的渠道(探测 /models 必失败但转发可用)勾选,如天翼云」。
- i18n: `web/src/i18n/` 中英文案各加一条(键名沿表单现有命名风格)。

## R2: 按添加时间排序

`web/src/components/modules/channel/index.tsx`:

1. `sortOptions` 增加两项(图标沿用现有风格,如 `ArrowDownWideNarrow`/`ArrowUpWideNarrow` 或时钟类图标):
   - `timeAsc` 按添加时间升序(最早在前)
   - `timeDesc` 按添加时间降序(最新在前)
2. 排序逻辑 `visibleChannels`: switch 分支——`timeAsc` 按 `a.channel_id - b.channel_id`,`timeDesc` 反之;原 `asc/desc` 名称排序保留。
3. store 兼容:`sortOrders.channel` 从二值扩四值,读取处 `=== 'desc' ? 'desc' : 'asc'` 的归一化要改为「不在已知集合时回落 'asc'」,避免把新值误折成 'asc' 之外的行为(现有代码本就回落 asc,新值回落 asc 可接受,但排序分支必须先判断新值再落到名称分支)。

不加 created_at 列:id 自增 ≈ 添加顺序,迁移保留 v1 主键,历史顺序正确(用户已确认方案)。

## 验证

- 单测:`ChannelHealthCandidates` 过滤(skip 渠道不出现)+ `ChannelUpdate` 恢复逻辑
- octopus-verify 全流程
- 手动:天翼云渠道勾选 → 健康检查轮后 fail_count 不增;列表按时间排序正确
