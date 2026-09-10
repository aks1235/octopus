# Research: 迁移影响(usage_cards / o_auth_sessions)

- **Query**: fork 表形状 vs migrate_v1_to_v2.py 已转换形状,是否一致、新表是否需补脚本
- **Scope**: internal(fork git + scripts/migrate_v1_to_v2.py)
- **Date**: 2026-09-09

## 1. fork 侧列清单(gorm 默认 snake_case)

**usage_cards**(`internal/model/usage_card.go`,73a13a2 建 + e873006 加 use_proxy):

```
id, name, template_id, account, endpoint, method, auth_type, auth_header,
encrypted_secret, extra_headers, config, enabled, use_proxy,
refresh_interval_sec, last_result, last_error, last_refresh_at,
created_at, updated_at
```

**o_auth_sessions**(`internal/provider/auth/session.go`,4d569fc 建):

```
id(char64 PK), provider_id(char64,ix), channel_id(int default 0),
session_data(text), result_data(text), status(char20,ix), state(char64,ix),
created_at, expires_at(ix)
```

## 2. 迁移脚本现状:两表已登记,零代码改动即可携带

`scripts/migrate_v1_to_v2.py`:

- :67 `CONDITIONAL_TABLES = ("relay_logs", "usage_cards", "o_auth_sessions")`
- :481-500 条件携带逻辑:**目标模板存在同名表 → 按 fork 列与 v2 列的交集 INSERT(保留主键)**;列差集进报告 dropped 清单
- :395-399 模板干净度校验:条件表在模板中存在时必须为空
- :396 注释:"当前模板无这三张表,检查自然跳过;**功能包任务落地后有表时生效**"

结论:**新表不需要改迁移脚本代码**。本包落地后重新生成迁移模板库(带空 usage_cards/o_auth_sessions),脚本自动开始携带。

## 3. 一致性核对与风险点

| 项 | 状态 | 说明 |
|---|---|---|
| 列名兼容 | **实现约束** | v2 的 UsageCard/OAuthSession 模型列名必须与 fork 完全一致(含 `encrypted_secret`/`use_proxy`/`refresh_interval_sec`/`session_data`/`result_data`),否则该列被交集逻辑丢弃并在报告标注 dropped |
| serializer:json 列 | 无碍 | extra_headers/config/last_result 在 SQLite 就是 TEXT 存 JSON 字符串,跨库直拷兼容 |
| 加密密钥 | **无风险(实况)** | fork `auth.SetEncryptionKey` 从未被调用,`Encrypt` 无密钥时原样返回明文(crypto.go:41-44)——**生产库 encrypted_secret/session_data 实际是明文**。v2 若实现真加密会造成新旧密文混杂;建议 v2 保持同样"无密钥=明文"行为或加启动期兼容判断 |
| 主键/自增 | 无碍 | 脚本 :502-521 已有 sqlite_sequence 修复(只升不降);usage_cards.id 是 uint 自增,在修复范围内 |
| o_auth_sessions 数据 | **建议不带数据** | 见 scope-triage.md T6:会话是临时态,fork 的 DBDump 都不含它;生产库只剩过期残留。表建了脚本就会带,但预期行数≈0;若 v2 改用设备码流可不建表 |
| channel_keys.key(Codex 凭证) | **自动复活路径** | 脚本对 channel_keys 丢 status_code/last_use_time_stamp/total_*/remark(:93-94),**保留 key 列**——fork 存的 Codex 凭证 JSON 原样进 v2 channel_keys.key。v2 落地 codex 支持后这些 key 即可用,前提是 JSON 形状与库解析兼容(fork CodexCredential: access_token/refresh_token/id_token/expires_at(RFC3339 字符串)/account_id/email;库 OAuthCredentials: access_token/refresh_token/id_token/expires_at(time.Time)/client_id/scopes——expires_at 类型差异,待实测,**待人工确认**) |
| usage_cards 模板引用 | 实现约束 | 迁移行带 template_id/account/last_result 等快照;v2 模板注册表需保留 fork 存量 template_id(至少 codex-usage 与 xfyun),否则卡片变孤儿。存量 template_id 分布需查 data/ 副本确认(**待人工确认**) |

## 4. 迁移脚本需要动的唯一潜在点

- 若设计决定 **o_auth_sessions 不建**(改设备码流):无动作,脚本继续跳过。
- 若建表但想**显式丢弃数据**:无需改码——把表建进模板后脚本会带数据;要避免带数据只能不建表或接受携带(行数≈0,可接受)。
- relay_logs 属日志持久化包的联动项,与本包无关。

## Caveats / 待人工确认

1. 生产库 `usage_cards` 存量行数与 template_id 分布(data/ 副本 `SELECT template_id, COUNT(*) FROM usage_cards GROUP BY 1`)——决定 v2 模板集
2. 生产库 `o_auth_sessions` 行数——预期 0/近 0,非 0 则需人工看内容
3. fork Codex 凭证 JSON 在库 `ParseCredentialsJSON` 下的解析结果(expires_at 字符串 vs time.Time 的 unmarshal 行为)——迁移后 key 可用性的硬前提
