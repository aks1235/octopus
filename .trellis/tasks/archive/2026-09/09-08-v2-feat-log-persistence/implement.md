# 执行计划:日志持久化包

> 工件顺序:prd.md → design.md → 本文件。每步完成后勾选并附验证结果。回滚点见各步尾。

## Step 1 后端底座:模型 + 建表 + 009 拦截 + 设置

- [x] 1.1 `internal/model/log.go`:自 dev 平移 RelayLog/ChannelAttempt/ChannelAttemptDetail/RelayLogDebugContent(完整列形状,含 user_agent/client_name/reasoning_effort)
- [x] 1.2 `internal/utils/snowflake`:自 dev 平移 Snowflake 生成器
- [x] 1.3 `internal/db/db.go` AutoMigrate 追加 `&model.RelayLog{}`
- [x] 1.4 `internal/db/migrate/009.go`:删 DROP relay_logs 段,注释引 ADR-0005
- [x] 1.5 `internal/model/setting.go`:+2 key(keep_enabled 默认 true / keep_period 默认 7),DefaultSettings + Validate(int 组)
- 验证:容器内 `go build`(spec 契约命令);全新 SQLite 库启动 → relay_logs 表存在且 009 幂等(AC1)
- 回滚点:revert 本步即可,无数据影响

## Step 2 op 层:缓冲/落盘/查询

- [x] 2.1 `internal/op/log.go`:RelayLogAdd(缓存+flush,去 subscribers)/RelayLogSaveDBTask(+cleanup)/RelayLogList/RelayLogGet/RelayLogClear/RelayLogAttemptsByChannel,自 dev 平移裁剪
- [x] 2.2 `internal/task/init.go`:注册 RelayLogSaveDBTask(仿 TaskStatsSave 模式,周期与 stats 落盘一致)
- 验证:单测(筛选组合、缓存+DB 合并分页、cleanup cutoff、AttemptsByChannel 粗筛+精确过滤+truncated);容器编译(注:缓存+DB 合并分页、LIKE 误匹配精确过滤、truncated 触顶三个用例为检查阶段补齐,补齐后容器 `go test ./internal/op/ ./internal/relay/` 全绿)
- 回滚点:revert Step1+2,无数据影响

## Step 3 relay 挂钩:attempts 插桩 + 终态组装

- [x] 3.1 `internal/relay/handler.go`:Forward 闭包本地 attempts 切片,每轮结果处 append(design §3.1;等待型 continue 不记)
- [x] 3.2 defer finalize:终态组装 RelayLog → op.RelayLogAdd(design §3.2;token/cost 取 RequestState 定稿值,ftut 近似并注释)
- [x] 3.3 单测:attempts 组装语义(成功/失败/多轮/等待轮)
- 验证:smoke 容器起 → 真实请求(成功/失败/流式/非流式)→ 查 relay_logs 行核验字段(AC2)
- 回滚点:revert Step3 后库内已产生的行为数据无碍(表结构不变)

## Step 4 接口层

- [x] 4.1 `internal/server/handlers/log.go` 追加:/list、/:id、/channel-attempts、/history/clear(design §4;上游 5 接口零改动)
- 验证:四接口 curl(带筛选参数组合)+ 鉴权组归属;SSE 既有接口回归(AC3/AC5 部分)
- 回滚点:与 Step3 一并 revert

## Step 5 前端:历史区 + 渠道调用详情 + 设置项

- [x] 5.1 `web/src/api/log-history.ts`:历史 hooks(list/channelAttempts/clearHistory),queryKey `['log-history']` 前缀(检查阶段修正:detail query 原用 `['log-detail']` 前缀,已并入 `['log-history', 'detail', ...]`,使清空历史的 invalidate 覆盖详情缓存;design 写的 `api/endpoints/log.ts` 是 fork 布局,本项目为扁平 `api/` 惯例)
- [x] 5.2 `web/src/components/ui/dialog.tsx`:radix 新建(零依赖既有 meta 包)
- [x] 5.3 平移:MultiSelect.tsx、channel/detail-store.ts、CallDetail.tsx、RequestDetailDialog.tsx(适配 dialog/JsonContent;保留自含 Dialog 绕 morph)
- [x] 5.4 `log/index.tsx`:tab 两区(实时=现列表零改动 / 历史=HistoryPanel:无限滚动+MultiSelect 筛选+LogCard);模型筛选项=groupsQueryOptions 分组名
- [x] 5.5 channel Card「调用详情」入口;设置页日志区块(开关+天数)
- [x] 5.6 i18n en/zh_hans/zh_hant
- 验证:前端容器 lint+build(spec 契约);smoke 容器人审 UI(AC3/AC4)
- 回滚点:前端文件独立,revert 无后端耦合

## Step 6 迁移链路验证(AC6)

- [x] 6.1 data 副本重跑 `scripts/migrate_v1_to_v2.py`:conditional_status 中 relay_logs 显示「转换」、零 dropped 列、行数对账 1285(规划期快照 1284,演练时新增 1 行,以实测为准)
- [x] 6.2 新库挂 smoke 容器启动正常,历史页可查到迁移行,attempts 四态展示正常(含 skipped/circuit_break 旧数据)
- 回滚点:演练用副本,不动生产 data/

## Step 7 收口

- [ ] 7.1 octopus-verify 全流程闭环(marker:容器编译/测试/前端/起容器/人审 UI)——AC8
- [x] 7.2 保留期实测:keep_period 调小触发 cleanup(AC7);keep_enabled 关闭→不落库→重开恢复(AC2 后半)
- [ ] 7.3 父任务 prd.md 任务地图勾选第 4 项;本任务 spec 沉淀(若有新契约,如 009 拦截惯例)走 trellis-update-spec
- [ ] 7.4 commit(单 commit 或按步分 commit,遵循仓库 conventional 风格)

## 全局回滚注意(design §8)

revert 含 009 改动的 commit 会恢复 DROP 段——**已有 relay_logs 数据的库(含迁移演练库)再启动将删表**。任何回滚操作前先备份库文件。

## 验证命令速查

构建/验证一律走 spec(build/dev-v2-build-deploy.md)容器契约命令 + octopus-verify skill;禁止本机 go/pnpm 直跑。
