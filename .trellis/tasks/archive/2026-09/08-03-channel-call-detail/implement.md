# 执行计划 — 渠道调用详情页

> 任务: `08-03-channel-call-detail` · 依 `prd.md` + `design.md`

## 实施顺序(checklist)

### 后端
- [x] 1. `internal/model/log.go` 新增 `ChannelAttemptDetail` 结构(字段见 design)。
- [x] 2. `internal/op/log.go` 实现 `RelayLogAttemptsByChannel(ctx, channelID, page, pageSize)`:
  - **关键(Review 发现 1):走 DB,不碰缓存**。`relayLogCache` 只是 ≤20 条的 flush 缓冲(`op/log.go:17` `relayLogMaxSize=20`),7 天历史**全在 DB**。`RelayLogList` 的"缓存优先"模式**不要照搬**到本任务——本任务按 attempt 展开,缓存只覆盖最近一屏,大头在 DB,必须直接查 `relay_logs` 表。
  - [x] 2a. 读 `relay_log_keep_period` 算 cutoff。
  - [x] 2b. `SELECT id,time,request_model_name,error,attempts FROM relay_logs WHERE time>=cutoff AND attempts LIKE ?` 粗筛(参数 `%"channel_id":<id>`,注意 JSON 序列化字段名匹配)。
    - **自检(Review 发现 2)**:写 LIKE 前先跑 `SELECT attempts FROM relay_logs LIMIT 1` 看真实序列化字节,确认模板 `%"channel_id":<id>%` 与实际格式匹配(jsoniter 输出 `channel_id":123` 带不带空格/字段名是否为 `channel_id`)。字段名取自 `ChannelAttempt` 的 JSON tag = `channel_id`(`model/log.go:27`),已核实;此步只验证字节细节,避免粗筛漏行。
  - [x] 2c. Go 层 unmarshal attempts → 过滤 `ChannelID==channelID` → 拼 `ChannelAttemptDetail`。
    - **口径(Review 发现 3)**:匹配用 `attempts[].ChannelID`(本次尝试渠道,int),**不要**引 `relay_logs.channel` 列(那是请求最终成功渠道,`model/log.go:44` JSON tag `channel`)。本期放 attempts 全量,不限最终渠道。
  - [x] 2d. 倒序 + 分页(内存游标,因粗筛后量受控) + truncated 上界(>5000 截断)。
  - [x] 2e. 单测 `internal/op/log_test.go`(若有) 或 `go test ./internal/op/...` 覆盖:匹配/分页/边界。
- [x] 3. `internal/server/handlers/log.go` 注册 `GET /api/v1/log/channel-attempts` + handler 解析 channel_id/page/page_size,调上一步。
- [x] 4. `go build ./...` 与 `go test ./internal/op/... ./internal/transformer/model/...` 通过。

### 前端
- [x] 5. `web/src/api/endpoints/log.ts` 加 `ChannelAttemptDetail` 类型 + `useChannelAttempts(channelID,page)` hook(infinite query)。
- [x] 6. `web/src/components/modules/channel/detail-store.ts` 新建 view zustand(`list`/`detail`)。
- [x] 7. `web/src/components/modules/channel/index.tsx` 接 view:detail 模式渲染 `ChannelCallDetail`,顶部加返回。
- [x] 8. `web/src/components/modules/channel/Card.tsx`(或 CardContent)加「调用详情」按钮 → setView detail。
- [x] 9. `web/src/components/modules/channel/CallDetail.tsx` 新建:概览统计 + 明细行 + 分页 + 点 request_id 打开详情。
- [x] 10. `web/src/components/modules/log/RequestDetailDialog.tsx` 新建:受控弹窗。**实现取舍**——log 页 `LogDetailPanels`/`DeferredJsonContent`/`ResponseLogContent` 均私有且依赖 `useMorphingDialog` context,抽出受控版解开 morph 耦合回归面大;改按 design「内容与 log 页一致」验收口径自含渲染:`getLogDetail` + 同款 JsonView 主题,**数据层用 react-query 替代手写 effect**(避免 react-compiler `set-state-in-effect` 报错),不动 Item.tsx 零回归。
- [x] 11. i18n messages 加 `channel.callDetail.*` 与 `log.channelAttempts.*`(en/zh_hans/zh_hant 三语,纯增量 +63 行无格式改动)。
- [x] 12. `pnpm run lint` + `pnpm run build` 通过(静态导出成功;新文件 0 error 0 warning,既有 error 非本任务引入)。

### 集成验证
- [x] 13. 启 octopus + 前端 dev:渠道页点某渠道「调用详情」进入。**(本地验证部署:跳过 deploy.sh 第 1 步 git pull,跑 2→3→4→5 用工作区代码构建,docker compose down/build/up 成功,容器 octopus-new 已起,绑 8080;版本号 v1.0.1-5-g3f5afc6 无 dirty)✅ 已点入**
- [x] 14. 概览统计 + 明细各行与 `scripts/qlog.py --by-channel <name>` 交叉核对一致(抽样)。**(自动化:Python 直查容器同库 data.db 对 channel_id=90 验证口径 — 粗筛行→精确条两步自洽,ChannelID==int 匹配正确;浏览器实证概览统计与明细数据正确)✅**
- [x] 15. 点某行 request_id → 就地弹该请求详情,内容与 log 页该条详情一致。**(浏览器实地点击已验证通过)✅**
- [x] 16. 切渠道 / 返回列表 / 翻页无异常;**(浏览器实测 OK)✅**
- [x] 17. 不破坏 log 页 / channel 列表原有功能(冒烟)。**(浏览器冒烟 OK)✅**

## 提交
- [x] feat: 渠道调用详情页(commit f551b95,12 文件 +758)
- [x] 已更新 .trellis/spec/backend/database-guidelines.md「relay_logs 历史查询走 DB 不走 relayLogCache + JSON 字段展开查询」契约(.trellis/ 被 git 忽略,本地留存)
- [ ] push(留待用户挑时辰:commit + push aks-fork,再按 octopus-deploy skill 打 tag/推镜像发版)

## 验证命令
```bash
# 后端
go build ./...
go test ./internal/op/... ./internal/transformer/model/...

# 前端
cd web && pnpm run lint && pnpm run build
```

## Review Gate(每完成一个里程碑自检)
- 后端接口鉴权 + 只读:确认挂在 `middleware.Auth()` 组、无写操作。
- **走 DB 不走缓存**:不经 `relayLogCache`(它只 ≤20 条 flush 缓冲,7 天数据在 DB),不就地改缓存对象。
- **LIKE 粗筛自检**:实现前已用 `SELECT attempts FROM relay_logs LIMIT 1` 核对 jsoniter 实际序列化字节,模板 `%\"channel_id\":<id>%` 匹配真实格式(含/不含空格)。
- **口径**:按 `attempts[].ChannelID`(本次尝试渠道)匹配,不引 `relay_logs.channel`(最终渠道)列。
- 三库一致:`attempts LIKE` 粗筛在 SQLite/MySQL/PG 行为一致(不依赖 dialect jsonb)。
- truncated 提示不静默截断。
- 不回归 log 页详情弹窗。

## 回退点
- 后端:删 handler + 函数即可(无 schema migration,只读)。
- 前端:view store 默认 list,detail 不可达即等同未上线。
