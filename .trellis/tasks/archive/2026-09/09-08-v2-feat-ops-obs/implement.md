# 执行计划

前置:prd.md(范围裁定)、design.md(技术设计)。按步执行,每步末尾跑该步验证;2.2 全 scope 检查后走 octopus-verify 闭环。

## Step 1 后端:健康检查(设计 §1)

- [x] `internal/model/channel.go`:Channel 4 列 + ChannelStats 4 只读字段(2026-09-09 子代理完成;op 取 ChannelHealthFail/Success 两显式函数 + ChannelHealthCandidates,design §1.5 允许按包内风格定)
- [x] `internal/model/setting.go`:2 个设置键 + 默认值(handlers/setting.go 已补 task.Update 接线)
- [x] `internal/probe/models.go`:从 handlers/channel.go 抽出探测逻辑,fetchModel 改调 probe 行为不变
- [x] `internal/op/channel.go`:健康更新 + ChannelStatsList 填充 + enabled=true 时清 auto_disabled
- [x] `internal/task/health.go`:ChannelHealthCheckTask + Init 注册(另加单轮 5 分钟总时限,防 deadline 掐断误计渠道失败)
- 验证:容器内 `go build ./... && go vet ./...`(构建命令照 .trellis/spec/build/dev-v2-build-deploy.md,勿忘 `-buildvcs=false`、宿主编二进制须 CGO_ENABLED=0)

## Step 2 后端:分组正则成员(设计 §2)

- [x] `internal/model/group.go`:Group.MemberRegex + Create/Update 请求字段
- [x] `internal/op/group.go`:GroupRegexSync + syncGroupItems 复用 + 4 个渠道 op 尾部触发 + Group Create/Update 即时重算
- [x] `internal/task/init.go`:group_regex_sync 兜底任务注册(5min,runOnStart=true)
- [x] `internal/server/handlers/group.go`:member_regex 收发 + regexp2 校验(坏正则 400)
- 验证:同 Step 1 通过;未新增路由(member_regex/健康设置走既有端点,原"路由打印"验证项自然不适用)

## Step 3 前端:健康徽标 + 设置项 + 分组正则编辑

- [x] channel.ts / group.ts / setting.ts 类型(2026-09-09 子代理完成)
- [x] 渠道 Card 健康徽标三态 + tooltip(健康态选择隐藏而非挂灰,避免持续视觉噪音)
- [x] 设置页两个数字输入(System.tsx 照 statsSaveInterval 通道)
- [x] 分组 Editor 正则输入 + 正则组只读成员列表 + 按正则吸纳按钮(预检用原生 RegExp,后端 regexp2 为权威校验)
- [x] i18n en/zh_hans/zh_hant 三语
- 验证:容器内 lint + tsc --noEmit + vite build 全过

## Step 4 ②⑤ 原生覆盖验证(R3)

- [x] smoke 容器实测级联删除与分组名/禁用态(verify_phase_ab.sh Phase A,5/5 PASS)
- [x] 记录 `research/native-coverage-verification.md` + result 文件

## Step 5 功能验收(AC1-AC7)

- [x] AC1/AC2:坏渠道2轮自动禁用→修复自动解禁(verify_phase_c.sh,实测通过)
- [x] AC3:人工禁用跨轮不被翻回;人工启用生效
- [x] AC4:间隔0任务移除(容器日志确认)
- [x] AC5/AC6:正则吸纳/多凭据全纳入/失配移除/删渠道清空/手动组零影响(10/10 PASS)
- [x] AC7:正则组 UI 只读 + 健康徽标人审(2026-09-09 用户确认通过)
- [x] 迁移演练影响说明:零影响(详见 research/native-coverage-verification.md 末节)

## Step 6 文档与任务地图(R4)

- [x] ADR-0002 追加「2026-09-09 勘误」节(2026-09-09 完成)
- [x] 任务4 prd 增加渠道调用详情页交付物(对照 f551b95)
- [x] 父任务 prd 地图更新(任务3 范围描述、任务4 补⑥)

## Step 7 收尾

- [x] 2.2 全 scope 质量检查(trellis-check:修复 GroupCreate 正则路径漏 sortGroupItems 一处契约缺陷+2 风格;4 观察留档裁定)
- [x] octopus-verify 闭环(后端 build/test 过、前端 lint/build 过、smoke 容器 200 无 fatal、人审 UI 确认、marker 已写)
- [x] 3.3 spec 更新:无需新增(构建契约已在 build spec;上游原生覆盖结论入 ADR-0002 勘误)/ 3.4 单 commit 提交

## 回滚点

- 每步验证失败:修复后重跑该步验证;代码层回滚 `git checkout -- <files>`
- 整体回滚:单 commit revert;DB 新列为增量列,残留无害
