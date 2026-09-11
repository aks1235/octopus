# e2e 冒烟资产(09-11-bump-axonhub-llm 沉淀)

多形状假上游 + 驱动断言,验证转发链行为等价性。依赖升级 / relay 改动后可复跑。

## 组成

- `sim.py` — 假上游(宿主直跑,仅标准库,端口 18080)。OpenAI 形 `/openai/v1/chat/completions` 与 Anthropic 形 `/anthropic/v1/messages`,按请求体 `model` 名(`sim-*` 场景)分发行为;含思考流、tool_calls 流、[DONE]、中途断连、500/零状态/空响应等形状。收到的请求全量记录,`GET /__recorded` 取、`GET /__reset` 清。
- `run_smoke.py` — 驱动。admin 登录 → 建渠道/分组/API Key → 逐项发客户端请求 → 对模拟器记录与网关响应做断言,输出逐项 PASS/FAIL;尾部等 65s 做日志终态断言(断连=failed、失败转移 attempts)。

## 跑法

```bash
# 1. 宿主起模拟器
python3 scripts/e2e/sim.py &

# 2. 起 8081 冒烟容器(verify 姿势,data-verify 隔离库,新库首启 admin/admin)
docker compose -f docker-compose.smoke.yml -f docker-compose.verify.yml up -d --build

# 3. 跑冒烟
python3 scripts/e2e/run_smoke.py            # GATEWAY 默认 http://127.0.0.1:8081
# 快速模式(跳过 65s 日志兜底断言):
SKIP_LOG_WAIT=1 python3 scripts/e2e/run_smoke.py
```

环境变量:`GATEWAY`(网关地址)、`SIM_LOCAL`(驱动访问模拟器,默认 127.0.0.1:18080)、`SIM_UPSTREAM`(容器内回连地址,缺省自动探测 compose 网络网关 `octopus-v2-smoke_default`)。

## 断言面

分组全部用 failover 模式(单成员也走选路);路由配置压到最快(失败 1 次即冷却 2s)。渠道 `health_check_skip=true` 避免健康检查干扰。

| 类别 | 用例 |
|---|---|
| 基线 | OpenAI 非流式透传 / json_schema 参数透传 / 思考流(reasoning_content + [DONE] 尾帧 + usage 帧) / tool_call 非流式与流式往返 / 空响应 / 零状态错误(行为记录) / 失败转移 / 同成员重试 / Anthropic 客户端跨协议 / Anthropic 流式事件序列 |
| 定向 | GLM 风格思考标记(reasoning_effort/thinking)透传 / web_search 原生工具保留(Anthropic 同协议透传 + Responses→Anthropic 转换两用例) / cache_control 断点透传 / OpenAI↔Anthropic 跨格式出站形状 / 多条 system 合并 |
| 加固 | 自定义头 `{client_header:...}` 占位符 / 流式中途断连(终态走日志断言) / 日志终态落库 |

## 注意

- `sim-openai-flaky` 计数随 `/__reset` 清零,驱动每个用例前都会 reset,重跑即干净。
- 日志终态断言依赖 relay_log 60s 兜底 flush(满 20 条提前),`SKIP_LOG_WAIT=1` 可跳过。
- data-verify 是一次性验证库,换新场景先备份删除再让首启重建(admin/admin)。
