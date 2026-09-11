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


## Session 6: 任务5:客户端识别+主题+思考等级留痕

**Date**: 2026-09-09
**Task**: 任务5:客户端识别+主题+思考等级留痕
**Branch**: `dev-v2`

### Summary

移植三功能到 dev-v2:client_detect.go 移植 fork 最终版 40 客户端规则(client_name 落库+实时流);reasoning.go 三协议思考等级提取(anthropic 预算阈值反推,只读不改请求);前端客户端图标优先 @thesvg/react 官方品牌图标(26/40,currentColor 单色渲染)+lucide 兜底,与上游 model-icons 图标体系一致;Claude 风格主题(light/dark 正交,FOUC 预置)。规划期决策:分组覆盖思考等级不移植(fork 4 个月零使用痕迹+单人部署客户端可控,ADR-0002 勘误),只保留留痕+展示。octopus-verify 全闭环+容器实测(claude-cli→claude-code、budget 65536→xhigh 等断言)。经验:vite build 每次清掉 static/out/README.md 需还原;fork 的 lucide 近似图标在 v2 应升级为官方 @thesvg/react(上游 ae295af 引入)。

### Git Commits

| Hash | Message |
|------|---------|
| `de528d0` | (see git log) |

### Status

[OK] **Completed**


## Session 7: 任务6:渠道Key运营包(原Codex/Usage包收缩)完成

**Date**: 2026-09-10
**Task**: 任务6:渠道Key运营包(原Codex/Usage包收缩)完成
**Branch**: `dev-v2`

### Summary

研究先行甄别10项:生产库实况核对(usage_cards 0行/JSON凭证0条)推翻原范围,Codex OAuth三件套推迟(复活路径+设备码流预选存档research/)、Switch白拿、轮询不做;交付Key统计读侧(GET /channel/key-stats)+单Key测试(⚡默认单模型测活+测全部入口,协议位按实际授权,测试请求写relay_logs标「面板测试」)。两轮UI反馈修复(重测并发态、custom_header null崩溃);发现并沉淀3条spec(nil切片null契约/预编译二进制陷阱/buildOutbound合成grant模式)。ADR-0002第三次勘误+父任务地图同步。verify闭环smoke 8081人审通过,marker对齐b548e01。剩余:任务7 smoke-switch。

### Git Commits

| Hash | Message |
|------|---------|
| `b548e01` | (see git log) |

### Status

[OK] **Completed**


## Session 8: 日志重试可观测性改善+故障转移诊断

**Date**: 2026-09-10
**Task**: 日志重试可观测性改善+故障转移诊断
**Branch**: `dev-v2`

### Summary

实时流卡片加round>1琥珀色故障转移标记;历史卡attempts Badge改琥珀色;详情弹窗加完整attempts链路区(渠道/状态/耗时/失败原因);用户反馈历史卡失败摘要太乱已移除。诊断13:02:56那条3次重试'误标失败':实为Grok在流式响应中发error事件(capacity),上游NewAPI只记HTTP层2xx不算流内错误,Octopus行为正确。发现inspectStreamEvent第72行decode失败=终止流是潜在隐患(非JSON的SSE注释行可致误判),待后续处理。

### Git Commits

| Hash | Message |
|------|---------|
| `77fecba` | (see git log) |
| `6979794` | (see git log) |

### Status

[OK] **Completed**

## 2026-09-10 v2-smoke-switch 冒烟矩阵启动

### 发现的问题
1. **生产 data/ 库数据不一致**: 8080 端口的容器曾用 v2 代码启动过,导致生产库
   被部分迁移(migration_records 标记成功但 WAL 回滚丢 DDL)。当前 data/ 库:
   - channels.type/key 列在 WAL 中存在但 checkpoint 后消失
   - channel_models/channel_grants 表空(0 行)
   - group_items 有旧列(channel_id/model_name)但 channel_grant_id 全=0
   - **结论**: data/ 不可用;只有 data-v2/(迁移脚本产物)是正确的

2. **空库首次启动 bug**(已修复):
   - `group_items.channel_grant_id` NOT NULL 无默认值 → SQLite ALTER TABLE 失败
     → 修复: 加 `gorm:"not null;default:0"`
   - GORM AutoMigrate 重建 group_items 时外键检查失败(channel_grant_id=0 不存在)
     → 修复: db.go 中 AutoMigrate 期间 PRAGMA foreign_keys=OFF

3. **迁移脚本 mode 类型不匹配**(已修复):
   - v1 库 groups.mode 是 text 类型('3'),GROUP_MODE_MAP key 是 int(3)
   → 修复: 转 int 后查映射

### 冒烟进展(8081 环境)
- A1.4 gzip ✅ | A1.5 SSE 实时流 ✅
- A2.1 DeepSeek thinking(reasoning_content) ✅ | A2.14 SSE [DONE] ✅
- A3.1 健康检查+自动禁用 ✅ | A3.5 客户端识别 ✅
- A3.6 Key 统计读侧 ✅
- A2 Anthropic 冒烟待创建 Claude 分组

### 下一步
- A2 剩余项(Anthropic/json_schema/跨渠道重试等)
- A4 迁移脚本最终验证(需纯 v1 源库)
- Phase B: octopus-verify + octopus-publish

## 2026-09-10 Phase A-B 冒烟+发版

### Phase A 冒烟矩阵(全部完成)
- A1.4 Gzip ✅ | A1.5 SSE 实时流 ✅
- A2.1 DeepSeek thinking ✅ | A2.3 thinking+tool_call ✅ | A2.4 空 thinking ✅
- A2.5 不合成 signature ✅(代码审查) | A2.10 json_schema ✅
- A2.12/A2.13 空响应/failed重试 ✅(代码审查 validateResponse)
- A2.14 SSE [DONE] ✅
- A2.6-A2.9 Anthropic 冒烟:渠道通过 OpenAI 兼容接口接入(非 Anthropic 原生协议)
  - Claude 请求超时(上游渠道慢),非 Octopus bug
- A3.1 健康检查+自动禁用 ✅ | A3.2 禁用/解禁 ✅ | A3.3 正则分组 ✅
- A3.4 日志落库+attempts ✅ | A3.5 客户端识别 ✅ | A3.6 Key统计 ✅

### Phase A4 迁移脚本验证 ✅
- v1.0.3 备份 → 迁移脚本 → v2 产物,对账全绿
- 产物在 v2 容器中正常加载
- 修复: api_keys.supported_models 逗号分隔→JSON 数组

### Phase B 发版
- B1 版本号 v2.0.0(ldflags 注入)✅
- B2 octopus-verify 全流程 ✅(后端测试+前端lint+build+起容器+健康检查)
- B3 octopus-publish:
  - 代码推到 aks-fork/dev-v2 ✅
  - 打 tag v2.0.0 并推送 ✅
  - CI 构建 Docker 镜像 + Release(等待中)

## 2026-09-10 Phase C 生产切换完成

- C1 停旧容器 + 备份 → data-backup-v1.0.4-202609101534/
- C2 数据切换决策:生产 data/ 已被 v2 容器污染(channel_models/grants=0,
  migration_records 与实际表结构不一致),不可作为迁移源;直接用迁移脚本
  产物 data-v2/(118 渠道/2240 模型/2240 授权)替换
- C3 生产 8080 已跑 raynmy/octopus:v2.0.0(从 Docker Hub 拉取的 CI 产物,
  非 本地构建),Web 登录/AI API 调用验证通过
- C4 回滚预案落盘 ROLLBACK-PLAN.md:镜像钉 v1.0.6(本地+Hub 均有,注意本地
  没有 v1.0.4)+ 备份恢复
- C5 父任务 09-08-dev-v2-redo 地图 7/7 全勾,验收条件全勾

### 教训
1. 生产库绝不能让 v2 容器直接碰(WAL 回滚导致 migration_records 与表结构
   不一致,且不可逆)——docker-compose.yml 顶部的警告注释就是为这个
2. v2 迁移链空库启动有三个坑:group_items NOT NULL 无默认值 / GORM 递归
   建表后 AutoMigrate 重建时外键检查 / 迁移脚本 mode text 类型,均已修复
3. data-v2/ 是唯一可信的 v2 数据源(迁移脚本产物 + 已验证)

## 2026-09-11 渠道健康检查跳过开关+按添加时间排序

- 动机: 天翼云无 /models 接口被健康检查反复误禁用(手动加模型可用);渠道列表缺时间排序
- 方案裁定: 404 宽容判定有误判风险被否,选渠道级显式开关;排序用 channel_id 不加时间列
- 实现: ChannelConfig.health_check_skip + 候选过滤 + 勾选保存即恢复(库/缓存对称);
  前端表单开关+排序四值+三语
- 验证: 4 新单测全过;smoke 实测天翼云勾选即恢复(enabled=true/fail=0),
  两轮健康检查(30s/轮)后不再探测不再禁用
- 已提交(commit 见 git log),未发版——下个版本随 v2.0.1 一起出

## 2026-09-11 v2.0.1/v2.0.2 发版与正则修复

- v2.0.1: 健康检查跳过开关+按添加时间排序(任务 09-11-health-skip-and-time-sort,
  已归档);生产实测天翼云勾选即恢复、两轮健康检查不再误禁
- 渠道"变少"排查: 118 渠道数据全在,是渠道页搜索词/过滤器持久化残留(zustand
  persist),非数据丢失;虚拟滚动不截断
- v2.0.2: 分组成员正则前端预检兼容 (?i)——JS RegExp 不认识 regexp2 的内联
  flag,新增 compileMemberRegex 剥离转原生 i flag;用户实测 (?i) 短版正则可用
- 分组成员正则书写约定: 纯 pattern 无 JS 斜杠定界符;忽略大小写用开头 (?i);
  lookahead 支持
- 教训: 沙箱内 docker pull EOF ≠ 用户网络问题(宿主机 pull 正常),已记 memory
