# Journal - yuan (Part 1)

> AI development session journal
> Started: 2026-07-30

---

## 2026-08-24 回滚:弃 v1.1.0,切回 dev + v1.0.4,等上游稳定后重做路线A

- 触发:用户反馈日志页转圈(15s+)与版本信息仍指向上游。排查定位:
  - SSE 转圈根因 = feat 线 `streamOverview` 空 snapshot 时响应头延迟至 15s 首次心跳才发出;dev 线 `9f8ad4d`(chenjh)有修复(`:connected`+Flush),rebase 到 upstream 线时丢失,属路线A回归。dev 分支自身不受影响。
  - 仓库地址 = version.go 默认值/build.sh 未注入 Repo/update.go 硬编码/Info.tsx 默认值,四处。
- 修复任务 08-21-fix-sse-spinning-and-repo-info:代码改完(6文件)、后端 build/vet/test + 前端 build 全过,镜像构建踩两个坑(漏 CGO_ENABLED=0 → alpine su-exec not found;vite outDir 在容器内解析为 /static/out 需显式挂载)后二进制已编好,**用户决定停止**——上游 v0.11.0(8-22)故障转移大重构仍在连续出修复,路线A基点已过时。
- 处置:工作区改动全部丢弃;任务标 abandoned 归档(prd/design 存完整修复方案待重做);切回 `dev` 分支;容器恢复 `raynmy/octopus:v1.0.4`(HTTP 200 正常,数据挂载 ./data)。
- feat/my-features 保留仅作参考。后续计划:等上游 v0.11.x 补丁收敛后重做路线A(上游故障转移白拿,⑦ 熔断器作废;详见 memory octopus-branch-strategy)。
- 遗留:dev 线 v1.0.4 banner 仍显示 Repo=chicring/built by chicring(update 指向已被 b7a7dd6 修为 aks1235,但 conf.Repo 默认值未改)。

## 2026-09-08 路线A重做启动:决策定案 + baseline-assets 完成

- grill-with-docs 拷问 8 题定案,8 条 ADR 入库 docs/adr/(基线/范围/多URL不作/uTLS不作/转换脚本迁移/v2.0.0/分批节奏/资产平移),术语表 docs/glossary.md;Trellis 父任务 09-08-dev-v2-redo + 7 子任务。
- baseline-assets 完成:dev-v2 = upstream v0.13.2(27aa40d) + 部署资产平移(deploy.sh 适配 vite:挂仓库根绕 outDir 坑、VITE_* env、golang 1.26)+ 仓库指向 4 处 aks1235 + 上游自带 lint 错修复 2 处。commit e86d8d9。smoke 8081 验证过(UI 人审),marker 已写。
- **事故**:smoke up 误杀生产容器——两 compose 同目录共用项目名+同服务名,compose 把 octopus-new 当旧实例重建。生产中断约 2-3 分钟,即时以 octopus:utls-test 原镜像+原参数恢复(数据无失)。修复:smoke compose 加 name: octopus-v2-smoke;主 compose 加警示头(迁移演练前禁 up,防 v2 迁移链 DROP relay_logs 打旧库)。教训入 spec。
- 关键事实:上游 v0.13.2 无任何 go 测试(40 个测试全被上游重构删除)→ dev-v2 无回归网,功能包全靠冒烟;上游 lint 自带 3 错(ItemList/chart.tsx),上游 CI 未跑 lint。
- 生产现状注意:线上实际跑 octopus:utls-test 镜像(9-4 uTLS 时代本地构建,代码已 revert 但镜像还在跑),非 compose 声明的 v1.0.6;切 v2.0.0 时自然替换。
- 下一步:v2-db-migration(输入 data-v2/data.db 15 表真实 schema 已拿到)可与功能包任务并行。


## Session 1: dev-v2 路线A启动:8 ADR决策 + baseline-assets完成

**Date**: 2026-09-08
**Task**: dev-v2 路线A启动:8 ADR决策 + baseline-assets完成
**Branch**: `dev-v2`

### Summary

grill-with-docs拷问8题定案,8条ADR入库,dev-v2基于upstream v0.13.2(27aa40d)建分支;baseline-assets完成(deploy.sh适配vite/仓库指向4处/CI平移/smoke compose独立项目名);生产容器事故复盘(同目录compose项目名冲突互杀,已加name隔离);冒烟8081通过,marker已写,e86d8d9提交。上游v0.13.2无测试、lint自带错已修。下一步v2-db-migration。

### Git Commits

| Hash | Message |
|------|---------|
| `e86d8d9` | (see git log) |

### Status

[OK] **Completed**


## Session 2: v2-db-migration 完成:fork→v2 转换脚本+演练+冒烟

**Date**: 2026-09-08
**Task**: v2-db-migration 完成:fork→v2 转换脚本+演练+冒烟
**Branch**: `dev-v2`

### Summary

交付 scripts/migrate_v1_to_v2.py(DB→DB,backup API 快照,逐表对账)。演练终态 117 渠道/2236 模型/10 组/233 组员,FK 0 违规,幂等 md5 一致;容器加载无迁移重跑,组9 真实上游选路 200。关键发现:①base_url /v1 后缀+axonhub 朴素拼接→/v1/v1 双叠 404→客户端挂死不报错(echo 实验证实,已归一化并沉淀 spec);②fork 路由本就精确名匹配,正则退化不存在,D2 终裁不建别名组;③条件携带路径用合成模板实测 1285 条可用。trellis-check 4 处修复(含 backup 重试窗口漏洞)。父任务地图 2/8。

### Git Commits

| Hash | Message |
|------|---------|
| `cc9f4a0` | (see git log) |

### Status

[OK] **Completed**


## Session 3: v2-feat-ops-obs 完成:健康检查+分组正则成员

**Date**: 2026-09-09
**Task**: v2-feat-ops-obs 完成:健康检查+分组正则成员
**Branch**: `dev-v2`

### Summary

任务3(运维刚需+可观测)范围经实地核对后重裁定(ADR-0002 勘误):上游 6e736b4 已删周期同步与自动分组,①重想象为渠道健康检查(internal/probe 抽探测共用,任一KEY×端点通=健康,阈值禁用/恢复解禁/人工接管),③重做为分组侧 member_regex 自动吸纳移除,②⑤验证上游原生覆盖(FK CASCADE+原生DTO)不移植,⑥渠道调用详情页挪任务4。AC1-7 全实测过(19/19 脚本断言+人审UI),trellis-check 修复正则建组漏 sortGroupItems 契约缺陷,octopus-verify 闭环 marker=f932754。父任务 3/7,下一个任务4(日志持久化,须拦 migrate/009 删表)。

### Git Commits

| Hash | Message |
|------|---------|
| `f932754` | (see git log) |

### Status

[OK] **Completed**


## Session 4: 任务4:日志持久化+渠道调用详情页移植完成

**Date**: 2026-09-09
**Task**: 任务4:日志持久化+渠道调用详情页移植完成
**Branch**: `dev-v2`

### Summary

完成 09-08-v2-feat-log-persistence 全流程:规划(prd/design/implement+双调研落盘)→实施(009 拦截、RelayLog 23 列、relay 终态 defer finalize 落库+attempts 插桩、历史查询四接口、前端同页 tab+渠道调用详情、i18n 三语、迁移脚本设置交集补带)→验证(13 单测、迁移演练 1285 条全量转换零丢列、cleanup/开关实测、SSE 回归、octopus-verify 闭环+两轮人审)。规划期发现渠道级禁用在选路链无检查(任务3 缺陷,自动禁用无实际止损),另立 09-09-v2-fix-channel-disable-routing。spec 沉淀 backend/relay-log-persistence.md(表形状冻结/回滚规程/条件携带迁移,任务5/6/7 依赖)。

### Git Commits

| Hash | Message |
|------|---------|
| `cfb1091` | (see git log) |
| `6d92dc4` | (see git log) |

### Status

[OK] **Completed**


## Session 5: 渠道禁用选路修复+容器实测闭环

**Date**: 2026-09-09
**Task**: 渠道禁用选路修复+容器实测闭环
**Branch**: `dev-v2`

### Summary

修复渠道级禁用不生效:ChannelGrantGet 补渠道 Enabled 校验,pickGroupItem 选路前过滤不可选成员(不选/不探测/不计数),亲和期内被禁立即失效重选,校验失败分支归还探测名额。新增 5 用例单测,沉淀 relay-routing.md spec。octopus-verify 全闭环+容器实测(禁用立即切流 keyAAA→keyBBB、恢复回选、全禁挂起不刷日志)。发现存量问题:./data/data.db 迁移 11 中途损坏(channel_models 缺失,11|failed 死循环),当前代码任何容器用该目录都起不来,需独立任务决策;本次验证改用 data-verify 隔离目录。

### Git Commits

| Hash | Message |
|------|---------|
| `9b42b6d` | (see git log) |
| `16d62c6` | (see git log) |

### Status

[OK] **Completed**
