# 执行计划:手动分组自动吸纳

前置:走 trellis-before-dev 载入 backend spec(重点 relay-routing.md 的 gorm 零值坑、api-serialization.md、database-guidelines.md)。

## 步骤

1. [x] `internal/op/group.go`:新增 `GroupManualAbsorb(ctx, channelIDs ...int)`(设计见 design.md;含组内吸纳私有函数,注释中文,口径注释引用前端 `matchesGroupName` 同源约定)
   - 验证:`go build ./...` + `go vet ./internal/op/`
2. [x] `internal/op/channel.go`:`ChannelCreate`/`ChannelUpdate` 的 `GroupRegexSync(ctx)` 之后追加 `GroupManualAbsorb(ctx, channelID)`
   - 验证:同上
3. [x] `internal/task/init.go`:`TaskGroupRegexSync` 回调串行追加 `GroupManualAbsorb(ctx)`(全量),更新注册处注释
   - 验证:`go build ./...`
4. [x] 单测 `internal/op/manual_group_absorb_test.go`(参照 `route_disable_test.go`/`channel_health_test.go` 的测试基建):
   - 新渠道命中 → 手动分组出现成员;未命中分组不动
   - 幂等:重复吸纳无重复行
   - 既有成员优先级不变,新成员追加尾部
   - 正则分组不被触碰
   - 删除成员后再触发 → 回归(锁定 append-only 语义)
   - channelIDs 过滤:非目标渠道的授权不吸纳
   - 验证:`go test ./internal/op/ -run Absorb -v`
5. [x] 全量回归:`go test ./...`(容器内,octopus-verify 第一步)
6. [x] octopus-verify 全流程(skill 硬要求,不可跳):容器内编译 → 后端测试 → 前端 lint+build → 起容器 → 健康检查 → **人审 UI**:新建测试渠道(模型名含既有手动分组名),确认分组自动出现成员;确认正则分组照旧
7. [x] spec 更新(trellis-update-spec):`.trellis/spec/backend/` 沉淀手动吸纳契约(口径、append-only、移除回归为预期、与 member_regex 的边界)
8. [ ] commit(中文 conventional;含任务归档)

## 回滚点

- 每步独立可 revert;整体回滚 = revert 功能 commit,无数据残留(吸纳产生的 group_items 行随分组正常生命周期管理,人工可在 UI 删除)。

## 风险提示

- 存量手动分组在首次触发/首轮兜底会吸纳存量匹配授权(任务目的本身),交付说明需提示。
- 短分组名(如 "gpt")吸纳面广,与编辑器按钮一致,用户自担。
