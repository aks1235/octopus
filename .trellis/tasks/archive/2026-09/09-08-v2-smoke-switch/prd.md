# 冒烟矩阵与停机切换 v2.0.0

## Goal

完成 dev-v2 最终验收与生产切换:按 ADR-0007 风险清单执行冒烟矩阵,确认上游白拿项+5 个功能包行为等价;然后执行停机切换,发布 v2.0.0。

## 前置条件(已满足)

- 子任务 1-6 全部完成并归档 ✅
- 迁移脚本 `scripts/migrate_v1_to_v2.py` 已演练对账通过 ✅
- 部署资产(deploy.sh / docker-compose / release.yaml)已平移适配 vite ✅
- 仓库指向已改 aks1235/octopus ✅

## Requirements

### R1: 上游白拿项冒烟

验证上游 v0.13.2 自带功能在新基线上正常可用:

- [ ] 多 Key 轮询(渠道绑定多个 API Key,请求自动轮换)
- [ ] failover(主渠道失败自动切换到备用渠道)
- [ ] gzip 压缩(请求/响应 gzip 透传)
- [ ] 日志页 SSE 实时流(不误报断开;上游 `10f0204` 已修,验证即可)

### R2: axonhub/llm 行为等价性冒烟(ADR-0007 核心风险区)

- [ ] DeepSeek thinking 族:非透传/透传回传、数组内容、工具调用 reasoning_content、空 thinking、不合成的 signature
- [ ] Anthropic 透传族:reasoning 参数、content_block_start 保留、thinking block 修补、缓存前缀不被透传复写破坏
- [ ] json_schema 在 Chat/Responses API 间序列化一致、`strict` 字段往返不丢
- [ ] 跨渠道重试时入站适配器状态隔离
- [ ] 上游 2xx 空响应 / Responses API failed/error 事件触发重试而非静默空响应
- [ ] SSE 全生命周期:流式 [DONE] 正常结束不误报断开

### R3: 功能包冒烟(逐包验证)

- [ ] ops-obs:渠道健康检查+失败自动禁用/解禁、分组正则成员
- [ ] log-persistence:日志落库+历史查询+详情页 attempts 链路
- [ ] client-theme:客户端识别+主题+思考等级留痕
- [ ] codex-usage:Key 级统计读侧+单 Key 测试

### R4: 迁移脚本最终验证

- [ ] 拿 `data/` 最新副本跑一遍完整转换,确认 113 渠道 + 1284 条日志 + 用户/设置全量转换无丢失
- [ ] 转换后新库在 v2 容器中加载正常,无 migration 错误

### R5: 停机切换与发版

- [ ] 版本号确认为 v2.0.0(ldflags 注入或代码默认值)
- [ ] 执行 octopus-verify 全流程(容器内编译→后端测试→前端 lint+build→起容器→健康检查)
- [ ] octopus-publish:打 tag v2.0.0 → 推 Docker Hub 镜像 → 更新 compose 版本号 → 推 GitHub → Release
- [ ] 生产切换:停 v1.0.4 → 备份 → 转换 → 起 v2 验证(回滚预案:镜像钉 v1.0.4 + 数据备份)

## Acceptance Criteria

- [ ] R1-R3 冒烟矩阵全项通过,不等价项已在新层重修或记录为已知偏差
- [ ] R4 迁移最终演练通过,无新增数据丢失
- [ ] R5 v2.0.0 已发布到 Docker Hub,CI 全绿
- [ ] 生产切换完成,线上跑 v2.0.0;回滚预案已演练或确认可行
- [ ] 父任务 `09-08-dev-v2-redo` 验收条件全部满足

## Constraints

- 冒烟矩阵手动执行(需真实 API Key 调用上游模型),不可全自动化
- 停机切换窗口以演练时长为基准,需提前通知
- 不追上游新 commit(ADR-0001),切换后再评估
- octopus-verify / octopus-publish 硬关卡,不跳过

## Notes

- 如果冒烟矩阵发现不等价行为,在本任务内修复(轻量)或另立子任务(重度)
- 迁移脚本已在子任务 2 演练过,本任务只做最终确认
- 发版后更新父任务 `09-08-dev-v2-redo` 的任务地图,将第 7 项标记完成
