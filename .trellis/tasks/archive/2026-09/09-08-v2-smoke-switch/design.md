# 冒烟矩阵与停机切换 v2.0.0 — 技术设计

## 边界

本任务不引入新功能代码,只做验证和发布。唯一可能改代码的场景:冒烟矩阵发现不等价行为,需在现有层内修复(轻量修复留在本任务;重度修复另立子任务)。

## 执行阶段

```
Phase A: 冒烟矩阵(本地容器)
  ├── A1: 上游白拿项冒烟
  ├── A2: axonhub/llm 行为等价性冒烟(ADR-0007 核心风险)
  ├── A3: 功能包冒烟
  └── A4: 迁移脚本最终验证

Phase B: 发版准备
  ├── B1: 版本号确认(v2.0.0)
  ├── B2: octopus-verify 全流程
  └── B3: octopus-publish(打 tag → 推镜像 → 推代码 → Release)

Phase C: 生产切换
  ├── C1: 停 v1.0.4 + 备份
  ├── C2: 迁移脚本跑正式库
  ├── C3: 起 v2 容器 + 验证
  └── C4: 回滚预案确认
```

## 冒烟矩阵设计

### 执行环境

- 本地 `docker compose up` 起容器(同 octopus-verify 环境)
- 用 `data-verify/` 隔离目录,不碰生产 `data/`
- 冒烟期间容器保持运行,通过 Web UI + API 验证

### A1: 上游白拿项

| 项目 | 验证方法 | 通过标准 |
|---|---|---|
| 多 Key 轮询 | 配置渠道绑定 2+ Key,发 3+ 请求,检查日志 Key 交替 | Key 出现轮换,无单 Key 连续 |
| failover | 配置主渠道用无效 Key,备渠道正常 | 请求自动切到备渠道,日志有 failover 记录 |
| gzip | curl 带 `Accept-Encoding: gzip` 请求 API | 响应含 `Content-Encoding: gzip` |
| SSE 实时流 | 打开日志页,发起请求,观察实时流 | 请求日志实时推送,流正常结束无断开误报 |

### A2: axonhub/llm 行为等价性(核心风险)

需要真实 API Key 调用,按 ADR-0007 清单逐项:

| 项目 | 验证方法 | 通过标准 |
|---|---|---|
| DeepSeek thinking 非透传 | 发请求到 DeepSeek thinking 渠道,检查响应含 reasoning_content | thinking 内容正确返回,非空数组 |
| DeepSeek thinking 透传回传 | 透传模式发请求 | reasoning_content 透传回客户端无丢失 |
| 工具调用 reasoning_content | DeepSeek + tool_use | reasoning_content 与 tool_call 共存不冲突 |
| 空 thinking | 发短 prompt 触发空 thinking | 空 thinking 不导致错误或空响应 |
| 不合成 signature | 检查代码:thinking signature 不被 fork 层伪造 | 代码审查+实际调用验证 |
| Anthropic reasoning 参数 | 透传 Claude extended thinking | reasoning 参数透传到上游,thinking block 正常返回 |
| content_block_start 保留 | 透传 Claude 流式 | content_block_start 事件保留,不被中间层吞掉 |
| thinking block 修补 | 部分思考+续传 | thinking block 修补逻辑正常 |
| 缓存前缀不被复写 | 启用 prompt caching + Claude 透传 | 缓存前缀在透传后仍有效,不被中间层改写破坏 |
| json_schema 一致性 | Chat + Responses API 都用 json_schema | 两边序列化结果一致,`strict` 字段往返保留 |
| 跨渠道重试适配器隔离 | 同一请求触发 failover 到不同供应商渠道 | 第二个渠道的适配器不继承第一个的状态/参数 |
| 2xx 空响应重试 | 模拟上游返回 200 空体 | 触发重试而非静默返回空 |
| Responses API failed/error | 模拟 Responses API 返回 failed/error 事件 | 触发重试而非静默 |
| SSE [DONE] 正常结束 | 长流式请求完整跑完 | [DONE] 正常接收,不误报断开 |

### A3: 功能包冒烟

| 功能包 | 验证方法 | 通过标准 |
|---|---|---|
| ops-obs:健康检查 | 容器启动后观察健康检查状态 | 健康检查自动运行,状态正确 |
| ops-obs:禁用/解禁 | 配置渠道连续失败阈值,手动触发失败 | 渠道自动禁用;等待后自动解禁 |
| ops-obs:分组正则 | 创建分组+正则规则,添加渠道 | 成员按正则自动匹配 |
| log-persistence | 发请求后查看历史日志 | 日志落库可查,详情页有 attempts 链路 |
| client-theme | 不同客户端(UA)发请求 | 识别正确,主题适配,thinking 等级留痕 |
| codex-usage:Key 统计 | 查看单 Key 统计页 | 统计数据正确显示 |
| codex-usage:Key 测试 | 点击单 Key 测试按钮 | 测试连通性返回正确结果 |

### A4: 迁移脚本最终验证

```bash
# 1. 复制生产 data/ 到 data-verify/
cp -r data/ data-verify/
# 2. 跑迁移脚本
python3 scripts/migrate_v1_to_v2.py --source data-verify/data.db --target data-verify/v2.db
# 3. 对账
#    - 渠道数 113
#    - 日志数 1284
#    - 用户/设置全量
# 4. 用 v2.db 起容器,确认无 migration 错误
```

## 发版设计

### 版本号

`internal/conf/version.go` 默认值已是 `"dev"`,实际版本号通过 CI ldflags 注入。release.yaml 已配好:
- `Version`: git tag (如 `v2.0.0`)
- `Commit`: git SHA
- `BuildTime`: 构建时间

需确认:打 tag `v2.0.0` 后 CI 能正确注入版本号。

### octopus-verify 流程

走 `/octopus-verify` skill 全流程(容器内编译→后端测试→前端 lint+build→起容器→健康检查),写 `.octopus-verified` marker。

### octopus-publish 流程

走 `/octopus-publish` skill 全流程,硬关卡:verify marker 必须存在。

## 生产切换设计(ADR-0005)

```
1. 通知:提前告知切换窗口
2. 停服:docker compose down (v1.0.4)
3. 备份:cp -r data/ data-backup-v1.0.4-$(date +%Y%m%d)/
4. 迁移:python3 scripts/migrate_v1_to_v2.py --source data/data.db --target data/v2.db
5. 切换:mv data/data.db data/data.db.v1.bak && mv data/v2.db data/data.db
6. 起服:修改 compose image 为 raynmy/octopus:v2.0.0 → docker compose up -d
7. 验证:API 调用 + UI 检查
8. 回滚(如需):compose 镜像改回 v1.0.4 + 恢复 data.db
```

## 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 冒烟发现不等价行为 | 延期发版 | 轻量修复留本任务;重度另立子任务 |
| DeepSeek/Anthropic API Key 过期或限额 | 无法完成 A2 冒烟 | 确认 Key 有效,必要时补充 |
| 迁移脚本在最新 data/ 上跑出问题 | 延期发版 | 演练先行,问题在切换前解决 |
| CI 发布失败 | 无镜像 | release.yaml 已验证过,重跑即可 |
