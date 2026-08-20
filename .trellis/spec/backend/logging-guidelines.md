# Logging Guidelines

> How logging is done in this project.

---

## Overview

Octopus 后端用 **`github.com/charmbracelet/log`**(结构化日志,支持彩色输出)。日志级别由 `data/config.json` 的 `log.level` 控制(`info` 默认,`debug` 排障)。容器内日志走 **stderr**(charmbracelet/log 默认),`docker logs` 能直接看到;stdout 基本只有启动 banner。

---

## Log Levels

| 级别 | 何时用 | 实例 |
|---|---|---|
| `Debug` | 任务起停、定时任务耗时、对账明细 | `log.Debugf("sync models task finished, sync time: %s", ...)` |
| `Info` | 启动、配置加载、状态机正向转换(如熔断器 Open→HalfOpen) | `log.Infof("circuit breaker [%s] Open -> HalfOpen ...", key)` |
| `Warn` | 熔断器跳闸(HalfOpen→Open 探测失败/Closed→Open)、上游 fetch 失败但可降级 | `log.Warnf("circuit breaker [%s] Closed -> Open ...", key)` |
| `Error` | 写库失败、任务内非致命 err(记日志后 continue,不崩) | `log.Errorf("failed to update sync status: %v", err)` |

**排序日志**:charmbracelet/log 输出已带级别前缀(`INFO`/`WARN`/`DEBU`),grep 时用全大写:
```bash
docker logs <容器> 2>&1 | grep -iE 'circuit breaker|WARN|ERROR'
```

---

## Structured Logging

- 用占位符 `{}`/`%s`/`%v`,**不字符串拼接**。charmbracelet/log 的 `Infof`/`Warnf`/`Errorf`/`Debugf` 走 fmt-style。
- 熔断器日志统一带 `[<circuitKey>]` 前缀(`<channelID>:<modelName>`,见 `balancer/circuit.go`),便于按渠道+模型 grep。
- key 名蛇形小写,值就近内联。

---

## What to Log

- **状态机转换**(熔断器三态迁移):每次 Open/HalfOpen/Closed 切换必 log,带 tripCount + cooldown——这是 smoke 验证的唯一观测点(⑦ 无单测)。
- **对账/同步任务**:起(started)+ 止(finished + cost)成对,中间记删了多少孤儿模型。
- **渠道同步失败**:带 `channel=name id=N count=X/threshold disabled=bool err=...`,可定位是哪个渠道在连续失败。

---

## What NOT to Log

- **上游 API key / Bearer token**:熔断/同步日志只带 channelID + name,**绝不带 `channel.Key`**。
- **完整请求体/响应体**:relay 日志存 `relay_logs` 表走 DB,不进 charmbracelet/log(量太大 + 可能含用户内容)。
- **PII**:用户请求内容、客户端原始 UA 长串不进 log(④ UA 只存解析后的 `ClientName` 短名)。

---

## 调试技巧

- 改 `data/config.json` 的 `log.level` 为 `debug` 后**重启容器**才生效(viper 读一次)。容器内改需 `docker exec ... sed`(文件 root 拥有)。
- relay 请求路径几乎所有内部状态都走 SSE(`log/overview/stream`)推前端,不走 charmbracelet/log 控制台——命令行日志主要看**定时任务 + 熔断器**。

---

## 相关

- 熔断器日志关键字 → `.trellis/tasks/archive/2026-08/08-19-circuit-breaker-port/design.md` §9
- [Quality Guidelines](./quality-guidelines.md) — GOTOOLCHAIN/go:embed 等构建铁律

