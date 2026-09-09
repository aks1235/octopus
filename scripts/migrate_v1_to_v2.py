#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""fork(v1.x 生产库) → dev-v2(v0.13.2 schema) 数据库转换脚本。

依据:
- docs/adr/0005-data-migration-by-conversion-script.md  转换脚本 / 停机一次切 / 统计不带
- docs/adr/0003-multi-url-not-ported.md                 每渠道取唯一 URL,不移植多 URL
- .trellis/tasks/09-08-v2-db-migration/design.md        逐表映射 #1-#11 与已定决策 D1/D2

用法:
    python3 scripts/migrate_v1_to_v2.py --src <fork库> --dst <产物库> --template <v2模板库>

流程:
    校验源库与模板库 → 备份式复制 template→dst(先删旧 dst) → 预清理(dst 演练用户)
    → 事务内逐表转换(显式保留 fork 主键) → 修复 sqlite_sequence → 外键自检 → 提交
    → 打印逐表对账报告。任一步失败整体回滚并删除 dst(宁可失败不可半成品)。

安全约束:
- 源库与模板库全程只读打开(file:...?mode=ro);模板经 SQLite backup API 复制,
  对 WAL 模式下的活库同样得到一致性副本
- 目标库先删后建,同输入同输出,可重复演练

退出码: 0=成功; 1=对账不一致; 2=执行错误(已回滚并清理 dst)
"""

import argparse
import json
import sqlite3
import sys
import time
from pathlib import Path

# base_urls[0].provider_id → v2 协议位掩码(见 internal/model/channel.go: 1<<1/1<<2/1<<3)
PROVIDER_PROTOCOLS = {
    "openai-chat": 2,
    "openai-response": 4,
    "anthropic": 8,
}

# fork settings 与 v2 settings 的交集键,仅这 4 键覆盖为 fork 值;
# 其余 8 键属功能包设置,随各自任务处理(design 映射 #10)
SETTINGS_INTERSECTION = (
    "proxy_url",
    "stats_save_interval",
    "cors_allow_origins",
    "model_info_update_interval",
    # 日志保留设置随日志持久化包(任务4)落地后加入交集, fork 侧用户的保留天数偏好随之带走
    "relay_log_keep_enabled",
    "relay_log_keep_period",
)

# v2 relay_config 六字段默认值(见 internal/model/group.go DefaultGroupRelayConfig)
RELAY_CONFIG_DEFAULTS = {
    "member_max_attempts": 2,
    "member_retry_interval_seconds": 3,
    "member_non_stream_response_timeout_seconds": 120,
    "member_stream_first_event_timeout_seconds": 30,
    "member_cooldown_seconds": 60,
    "member_affinity_seconds": 300,
}

# fork GroupMode → v2 GroupMode;D1 已定: 轮询(1)降级为 failover 并在报告标注
# fork 2=随机 / 4=加权 在生产库不存在,出现即报错中止
GROUP_MODE_MAP = {3: ("failover", ""), 1: ("failover", "轮询降级: 丢负载分摊,成员顺序=原优先级")}

# 条件携带表: 目标库(模板)存在才转换,否则跳过并报告(功能包任务落地后自动生效)
CONDITIONAL_TABLES = ("relay_logs", "usage_cards", "o_auth_sessions")

# fork 侧必须存在的表;缺失说明不是有效的 fork 库
FORK_REQUIRED_TABLES = (
    "channels", "channel_keys", "groups", "group_items",
    "api_keys", "users", "settings", "llm_infos",
)

# v2 侧(模板)必须存在的表;缺失说明模板库不是迁移链跑完的 v2 库
V2_REQUIRED_TABLES = (
    "channels", "channel_keys", "channel_models", "channel_grants",
    "groups", "group_items", "api_keys", "users", "settings",
    "llm_infos", "migration_records",
)

# 模板库这些表必须为空,否则模板不干净(应重新生成),直接报错而非混装
V2_CARRY_TABLES_MUST_BE_EMPTY = (
    "channels", "channel_keys", "channel_models", "channel_grants",
    "groups", "group_items", "api_keys", "llm_infos",
)

# 报告尾部的丢弃字段清单(与 design.md 映射表一致,静态说明)
DROPPED_FIELDS_SUMMARY = (
    "channels      : model, custom_model, auto_sync, auto_group, sync_fail_count,"
    " last_sync_error, last_sync_at, auto_disabled, type, base_url(旧列), key, utls;"
    " base_urls 取 [0].url 写入 base_url",
    "channel_keys  : status_code, last_use_time_stamp, total_cost, total_requests,"
    " total_input_token, total_output_token, remark",
    "groups        : match_regex, reasoning_effort_override;"
    " first_token_time_out/retry_interval/session_keep_time 语义并入 relay_config",
    "group_items   : weight",
    "llm_infos     : context_length, max_output_tokens",
    "settings      : 其余 6 键属功能包(sync_llm_interval/circuit_breaker_*/"
    "sync_fail_threshold/group_reconcile_interval)",
    "统计          : fork 8 张 stats 历史表不带(ADR-0005),渠道/凭据/模型统计列全部置 0",
)


class ConvertError(Exception):
    """转换失败: 整体回滚并删除 dst,退出码 2。"""


def open_ro(path):
    """以只读模式打开 SQLite 库(URI mode=ro),防止任何误写。"""
    uri = Path(path).absolute().as_uri() + "?mode=ro"
    return sqlite3.connect(uri, uri=True)


def table_names(con):
    return {r[0] for r in con.execute(
        "SELECT name FROM sqlite_master WHERE type='table'")}


def table_columns(con, table):
    return [r[1] for r in con.execute(f"PRAGMA table_info([{table}])")]


def table_count(con, table):
    return con.execute(f"SELECT COUNT(*) FROM [{table}]").fetchone()[0]


def remove_db_files(path):
    """删除目标库及其 journal/WAL 伴随文件(先删后建的『删旧』)。"""
    for suffix in ("", "-journal", "-wal", "-shm"):
        p = Path(str(path) + suffix)
        if p.exists():
            p.unlink()


def fetch_rows(con, sql, params=()):
    con.row_factory = sqlite3.Row
    return [dict(r) for r in con.execute(sql, params)]


def build_relay_config(first_token_time_out, retry_interval, session_keep_time):
    """fork 三字段 + v2 默认 → 显式六字段 relay_config JSON(design 映射 #8)。

    0 或负值取 v2 默认;session_keep_time=0 是合法值(不保持亲和),直接保留。
    字段按固定顺序序列化,保证同输入同输出。
    """
    cfg = {
        "member_max_attempts": RELAY_CONFIG_DEFAULTS["member_max_attempts"],
        "member_retry_interval_seconds": (
            retry_interval if retry_interval and retry_interval > 0
            else RELAY_CONFIG_DEFAULTS["member_retry_interval_seconds"]),
        "member_non_stream_response_timeout_seconds":
            RELAY_CONFIG_DEFAULTS["member_non_stream_response_timeout_seconds"],
        "member_stream_first_event_timeout_seconds": (
            first_token_time_out if first_token_time_out and first_token_time_out > 0
            else RELAY_CONFIG_DEFAULTS["member_stream_first_event_timeout_seconds"]),
        "member_cooldown_seconds": RELAY_CONFIG_DEFAULTS["member_cooldown_seconds"],
        "member_affinity_seconds": (session_keep_time or 0),
    }
    return json.dumps(cfg, ensure_ascii=False, separators=(",", ":"))


def load_source(src):
    """读入 fork 库全部待转换数据并做前置校验,返回内存模型。"""
    tables = table_names(src)
    missing = [t for t in FORK_REQUIRED_TABLES if t not in tables]
    if missing:
        raise ConvertError(f"源库缺少 fork 必备表: {', '.join(missing)}(不是有效的 fork 库?)")

    data = {}
    data["users"] = fetch_rows(src, "SELECT * FROM users ORDER BY id")
    data["api_keys"] = fetch_rows(src, "SELECT * FROM api_keys ORDER BY id")
    data["llm_infos"] = fetch_rows(src, "SELECT * FROM llm_infos ORDER BY name")
    data["fork_settings"] = fetch_rows(src, "SELECT key, value FROM settings ORDER BY key")
    data["channels"] = fetch_rows(src, "SELECT * FROM channels ORDER BY id")
    data["keys"] = fetch_rows(src, "SELECT * FROM channel_keys ORDER BY id")
    data["groups"] = fetch_rows(src, "SELECT * FROM groups ORDER BY id")
    data["group_items"] = fetch_rows(src, "SELECT * FROM group_items ORDER BY id")

    # ---- channels: 取唯一 URL(ADR-0003),协议位未知即中止 ----
    chan_models = {}   # channel_id -> [模型名](保留清单顺序,去空白去重)
    chan_protocol = {}  # channel_id -> 协议位
    chan_base_url = {}
    normalize_count = 0  # base_url 归一化(去结尾 /v1)的渠道数
    for ch in data["channels"]:
        try:
            base_urls = json.loads(ch["base_urls"]) if ch["base_urls"] else []
        except (TypeError, ValueError) as e:
            raise ConvertError(f"渠道 {ch['id']} base_urls 不是有效 JSON: {e}")
        if len(base_urls) != 1:
            raise ConvertError(
                f"渠道 {ch['id']}({ch['name']}) base_urls 数量为 {len(base_urls)},"
                f"仅支持恰好 1 个(ADR-0003 取唯一 URL)")
        provider = base_urls[0].get("provider_id")
        if provider not in PROVIDER_PROTOCOLS:
            raise ConvertError(
                f"渠道 {ch['id']}({ch['name']}) provider_id={provider!r} 无协议位映射,中止")
        url = base_urls[0].get("url")
        if url is None:
            raise ConvertError(f"渠道 {ch['id']}({ch['name']}) base_urls[0].url 为空,中止")
        # 归一化: 去掉结尾 /v1 —— axonhub 对 BaseURL+EndpointPath 朴素拼接,
        # v2 各协议路径自带 /v1 前缀, fork URL 的 /v1 后缀会拼出 /v1/v1/... 双叠路径(echo 实验实证)
        normalized = url.rstrip("/")
        if normalized.endswith("/v1"):
            normalized = normalized[: -len("/v1")]
        if normalized != url.rstrip("/"):
            normalize_count += 1
        chan_protocol[ch["id"]] = PROVIDER_PROTOCOLS[provider]
        chan_base_url[ch["id"]] = normalized

        names, seen = [], set()
        for part in (ch["model"] or "").split(","):
            part = part.strip()
            if part and part not in seen:
                seen.add(part)
                names.append(part)
        chan_models[ch["id"]] = names

    # ---- 孤儿补全: group_items 引用但渠道清单缺失的模型(design 映射 #5) ----
    orphan_notes = []
    orphan_by_channel = {}
    for gi in data["group_items"]:
        cid, mname = gi["channel_id"], gi["model_name"]
        if cid not in chan_models:
            raise ConvertError(f"group_item {gi['id']} 引用不存在的渠道 {cid},中止")
        if mname not in chan_models[cid] and mname not in orphan_by_channel.setdefault(cid, []):
            orphan_by_channel[cid].append(mname)
            orphan_notes.append((gi["id"], gi["group_id"], cid, mname))
    for cid, extra in orphan_by_channel.items():
        chan_models[cid].extend(extra)  # 追加在渠道自身清单之后

    # ---- channel_keys: 设计假定每渠道恰好 1 把(name='default' 渠道内唯一) ----
    keys_by_channel = {}
    for k in data["keys"]:
        keys_by_channel.setdefault(k["channel_id"], []).append(k)
    for cid, ks in keys_by_channel.items():
        if len(ks) > 1:
            raise ConvertError(
                f"渠道 {cid} 有 {len(ks)} 把凭据,映射假定每渠道恰好 1 把(name='default'),中止")

    # ---- 派生表: channel_models / channel_grants,显式分配自增主键 ----
    model_ids = {}    # (channel_id, model_name) -> channel_models.id
    models_rows = []  # 待插入行
    grant_ids = {}    # (channel_id, model_name, key_id) -> channel_grants.id
    grants_rows = []
    next_id = 1
    for ch in data["channels"]:
        cid = ch["id"]
        for mname in chan_models[cid]:
            models_rows.append({"id": next_id, "channel_id": cid, "name": mname})
            model_ids[(cid, mname)] = next_id
            next_id += 1
    next_id = 1
    for ch in data["channels"]:
        cid = ch["id"]
        for mname in chan_models[cid]:
            for k in keys_by_channel.get(cid, []):
                grants_rows.append({
                    "id": next_id,
                    "channel_model_id": model_ids[(cid, mname)],
                    "channel_key_id": k["id"],
                    "protocols": chan_protocol[cid],
                })
                grant_ids[(cid, mname, k["id"])] = next_id
                next_id += 1

    # ---- groups: 模式映射 + relay_config(D1) ----
    group_rows = []
    degraded_poll = []  # 轮询降级清单
    group_relay_json = {}
    for g in data["groups"]:
        if g["mode"] not in GROUP_MODE_MAP:
            raise ConvertError(
                f"分组 {g['id']}({g['name']}) mode={g['mode']} 无映射"
                f"(仅支持 3=故障转移/1=轮询),中止")
        mode, note = GROUP_MODE_MAP[g["mode"]]
        if note:
            degraded_poll.append((g["id"], g["name"]))
        cfg_json = build_relay_config(
            g.get("first_token_time_out"), g.get("retry_interval"),
            g.get("session_keep_time"))
        group_relay_json[g["id"]] = cfg_json
        group_rows.append({
            "id": g["id"], "name": g["name"], "mode": mode,
            "active_item_id": g.get("active_item_id") or 0,
            "relay_config": cfg_json,
        })

    # ---- group_items: (channel_id, model_name) → 其唯一 grant(design 映射 #9) ----
    item_rows = []
    for gi in data["group_items"]:
        cid, mname = gi["channel_id"], gi["model_name"]
        ks = keys_by_channel.get(cid, [])
        if not ks:
            raise ConvertError(
                f"group_item {gi['id']} 引用渠道 {cid} 的 {mname!r},该渠道无凭据,无法生成授权,中止")
        grant_id = grant_ids.get((cid, mname, ks[0]["id"]))
        if grant_id is None:
            raise ConvertError(
                f"group_item {gi['id']} 引用 ({cid}, {mname!r}) 无法解析到渠道模型,中止")
        item_rows.append({
            "id": gi["id"], "group_id": gi["group_id"],
            "channel_grant_id": grant_id, "priority": gi["priority"] or 0,
        })

    # ---- D2(2026-09-08 用户终裁:不建别名组)----
    # 请求模型名扫描仅作报告:凡 v2 精确名未命中的名字迁移后仍 404,与 fork 行为严格一致。
    # (fork 路由本就是精确名匹配,此类名字在 fork 上也是失败请求,故不属行为退化)
    regex_scan = []
    if "relay_logs" in tables:
        req_names = [r[0] for r in src.execute(
            "SELECT DISTINCT request_model_name FROM relay_logs"
            " WHERE request_model_name IS NOT NULL ORDER BY request_model_name")]
        v2_names = {g["name"] for g in group_rows}
        for name in req_names:
            exact = name in v2_names
            total = src.execute(
                "SELECT COUNT(*) FROM relay_logs WHERE request_model_name=?",
                (name,)).fetchone()[0]
            failed = src.execute(
                "SELECT COUNT(*) FROM relay_logs WHERE request_model_name=?"
                " AND error IS NOT NULL AND error!=''", (name,)).fetchone()[0]
            if exact:
                decision = "v2 精确名命中"
            else:
                decision = "v2 精确名未命中(迁移后仍 404,与 fork 一致)"
            regex_scan.append((name, total, failed, decision))

    data.update({
        "chan_models": chan_models,
        "chan_protocol": chan_protocol,
        "chan_base_url": chan_base_url,
        "models_rows": models_rows,
        "grants_rows": grants_rows,
        "group_rows": group_rows,
        "item_rows": item_rows,
        "orphan_notes": orphan_notes,
        "normalize_count": normalize_count,
        "degraded_poll": degraded_poll,
        "regex_scan": regex_scan,
    })
    return data


def copy_template_to_dst(template, dst):
    """备份式复制 template→dst(先删旧 dst)。

    用 SQLite backup API 而非文件复制: 模板若处于 WAL 模式且被应用占用,
    直接 cp 主文件可能拿到不含 WAL 的不一致副本;backup 从只读连接取一致性快照。
    模板被应用占用时, ro 打开与对方 checkpoint/-shm 重建存在瞬时竞态
    (表现为 disk I/O error), 重试即可; 正式切换时应用已停, 天然无此问题。
    """
    Path(dst).parent.mkdir(parents=True, exist_ok=True)
    remove_db_files(dst)
    last_err = None
    for _ in range(5):
        tpl = out = None
        try:
            tpl = open_ro(template)  # ro 打开也在同一竞态窗口内,失败同样纳入重试
            out = sqlite3.connect(str(dst))
            tpl.backup(out)
            return
        except sqlite3.OperationalError as e:
            last_err = e
            remove_db_files(dst)
            time.sleep(0.5)
        finally:
            for con in (out, tpl):
                if con is not None:
                    con.close()
    raise ConvertError(f"复制模板失败(已重试 5 次): {last_err}")


def convert(src_path, dst_path, template_path):
    """执行完整转换,返回对账所需内存模型 data;失败抛 ConvertError 并清理 dst。"""
    src = open_ro(src_path)
    dst = None
    try:
        # 源库前置探测: 打不开/不是 SQLite 在此给出清晰报错
        table_names(src)
        data = load_source(src)

        copy_template_to_dst(template_path, dst_path)
        dst = sqlite3.connect(str(dst_path), isolation_level=None)
        dst.execute("PRAGMA foreign_keys=OFF")  # 转换按依赖序插入,自检统一走 foreign_key_check

        # 模板干净度校验(必须为空,users 预清理例外)
        dst_tables = table_names(dst)
        missing = [t for t in V2_REQUIRED_TABLES if t not in dst_tables]
        if missing:
            raise ConvertError(f"模板库缺少 v2 必备表: {', '.join(missing)}")
        for t in V2_CARRY_TABLES_MUST_BE_EMPTY:
            if table_count(dst, t) != 0:
                raise ConvertError(f"模板库表 {t} 非空,模板不干净,请重新生成模板")
        # 条件携带表若存在于模板,同样必须为空: 防止模板残留行与 fork 行混装
        # (当前模板无这三张表,检查自然跳过;功能包任务落地后有表时生效)
        for t in CONDITIONAL_TABLES:
            if t in dst_tables and table_count(dst, t) != 0:
                raise ConvertError(f"模板库表 {t} 非空,模板不干净,请重新生成模板")
        dst.execute("DELETE FROM users")  # 预清理: 模板含演练期用户

        dst.execute("BEGIN IMMEDIATE")

        # #1 users(原样)
        dst.executemany(
            "INSERT INTO users (id, username, password) VALUES (?,?,?)",
            [(u["id"], u["username"], u["password"]) for u in data["users"]])

        # #2 api_keys(原样全列)
        dst.executemany(
            "INSERT INTO api_keys (id, name, api_key, enabled, expire_at, max_cost,"
            " supported_models) VALUES (?,?,?,?,?,?,?)",
            [(a["id"], a["name"], a["api_key"], a["enabled"], a["expire_at"],
              a["max_cost"], a["supported_models"]) for a in data["api_keys"]])

        # #3 llm_infos(丢 context_length/max_output_tokens)
        dst.executemany(
            "INSERT INTO llm_infos (name, input, output, cache_read, cache_write)"
            " VALUES (?,?,?,?,?)",
            [(i["name"], i["input"], i["output"], i["cache_read"], i["cache_write"])
             for i in data["llm_infos"]])

        # #4 channels(取唯一 URL,dialect=generic,协议路径留默认,统计置 0)
        dst.executemany(
            "INSERT INTO channels (id, name, dialect, enabled, base_url, proxy,"
            " channel_proxy, custom_header, param_override, match_regex,"
            " input_token, output_token, input_cost, output_cost, wait_time,"
            " request_success, request_failed)"
            " VALUES (?,?,?,?,?,?,?,?,?,?,0,0,0,0,0,0,0)",
            [(c["id"], c["name"], "generic", c["enabled"],
              data["chan_base_url"][c["id"]], c["proxy"], c["channel_proxy"],
              c["custom_header"], c["param_override"], c["match_regex"])
             for c in data["channels"]])

        # #6 channel_keys(channel_key→key,name='default',统计置 0)
        dst.executemany(
            "INSERT INTO channel_keys (id, channel_id, name, key, enabled,"
            " input_token, output_token, input_cost, output_cost, wait_time,"
            " request_success, request_failed) VALUES (?,?,?,?,?,0,0,0,0,0,0,0)",
            [(k["id"], k["channel_id"], "default", k["channel_key"], k["enabled"])
             for k in data["keys"]])

        # #5 channel_models(拆分 + 孤儿补全,统计置 0)
        dst.executemany(
            "INSERT INTO channel_models (id, channel_id, name,"
            " input_token, output_token, input_cost, output_cost, wait_time,"
            " request_success, request_failed) VALUES (?,?,?,0,0,0,0,0,0,0)",
            [(m["id"], m["channel_id"], m["name"]) for m in data["models_rows"]])

        # #7 channel_grants(模型×凭据,protocols=渠道协议位)
        dst.executemany(
            "INSERT INTO channel_grants (id, channel_model_id, channel_key_id,"
            " protocols) VALUES (?,?,?,?)",
            [(g["id"], g["channel_model_id"], g["channel_key_id"], g["protocols"])
             for g in data["grants_rows"]])

        # #8 groups
        dst.executemany(
            "INSERT INTO groups (id, name, mode, active_item_id, relay_config)"
            " VALUES (?,?,?,?,?)",
            [(g["id"], g["name"], g["mode"], g["active_item_id"], g["relay_config"])
             for g in data["group_rows"]])

        # #9 group_items
        dst.executemany(
            "INSERT INTO group_items (id, group_id, channel_grant_id, priority)"
            " VALUES (?,?,?,?)",
            [(i["id"], i["group_id"], i["channel_grant_id"], i["priority"])
             for i in data["item_rows"]])

        # #10 settings: 仅交集 4 键覆盖为 fork 值
        fork_settings = {s["key"]: s["value"] for s in data["fork_settings"]}
        carried = [k for k in SETTINGS_INTERSECTION if k in fork_settings]
        for key in carried:
            dst.execute(
                "INSERT INTO settings (key, value) VALUES (?,?)"
                " ON CONFLICT(key) DO UPDATE SET value=excluded.value",
                (key, fork_settings[key]))
        data["settings_carried"] = carried

        # #11 条件携带: 目标库存在才转换(列取交集,保留主键),否则跳过
        conditional_status = []
        for t in CONDITIONAL_TABLES:
            src_has = t in table_names(src)
            src_n = table_count(src, t) if src_has else 0
            if t in dst_tables:
                src_cols = table_columns(src, t) if src_has else []
                common = [c for c in src_cols if c in table_columns(dst, t)]
                dropped = [c for c in src_cols if c not in table_columns(dst, t)]
                rows = fetch_rows(src, f"SELECT * FROM [{t}] ORDER BY rowid") if src_has else []
                if rows:
                    cols_sql = ", ".join(f"[{c}]" for c in common)
                    marks = ", ".join("?" for _ in common)
                    dst.executemany(
                        f"INSERT INTO [{t}] ({cols_sql}) VALUES ({marks})",
                        [tuple(r[c] for c in common) for r in rows])
                conditional_status.append((t, src_n, table_count(dst, t), "转换", dropped))
            else:
                conditional_status.append((t, src_n, "-", "跳过: 目标模板无此表", []))
        data["conditional_status"] = conditional_status

        # ---- sqlite_sequence 修复: 每张自增表 seq >= max(id),只升不降 ----
        auto_tables = [name for name, sql in dst.execute(
            "SELECT name, sql FROM sqlite_master WHERE type='table' AND sql IS NOT NULL")
            if "AUTOINCREMENT" in sql]
        for t in auto_tables:
            if "id" not in table_columns(dst, t):
                continue
            max_id = dst.execute(
                f"SELECT COALESCE(MAX(id), 0) FROM [{t}]").fetchone()[0]
            row = dst.execute(
                "SELECT seq FROM sqlite_sequence WHERE name=?", (t,)).fetchone()
            cur = row[0] if row else 0
            target = max(cur, max_id)
            if row is None:
                if max_id > 0:
                    dst.execute(
                        "INSERT INTO sqlite_sequence (name, seq) VALUES (?,?)", (t, target))
            elif target > cur:
                dst.execute(
                    "UPDATE sqlite_sequence SET seq=? WHERE name=?", (target, t))

        # ---- 外键自检: 有不可解析引用即整体失败(宁可失败不可半成品) ----
        fk_violations = dst.execute("PRAGMA foreign_key_check").fetchall()
        if fk_violations:
            raise ConvertError(f"外键自检失败: {fk_violations[:10]}")

        dst.execute("COMMIT")
        dst.execute("PRAGMA wal_checkpoint(TRUNCATE)")  # 让 dst 成为自洽单文件产物
        return data
    except Exception as e:
        if dst is not None:
            try:
                dst.execute("ROLLBACK")
            except sqlite3.Error:
                pass
            dst.close()
        remove_db_files(dst_path)
        src.close()
        if isinstance(e, ConvertError):
            raise
        raise ConvertError(f"{type(e).__name__}: {e}") from e
    finally:
        if dst is not None:
            try:
                dst.close()
            except sqlite3.Error:
                pass
        src.close()


def print_report(data, dst_path):
    """打印逐表对账报告,返回不一致项列表(非空 → exit 1)。"""
    dst = open_ro(dst_path)
    src_counts = {
        "users": len(data["users"]),
        "api_keys": len(data["api_keys"]),
        "llm_infos": len(data["llm_infos"]),
        "channels": len(data["channels"]),
        "channel_keys": len(data["keys"]),
        "groups": len(data["groups"]),
        "group_items": len(data["group_items"]),
    }
    # 拆分基数 = 渠道清单拆分合计(chan_models 已含孤儿补入,扣除即得)
    split_base = sum(len(v) for v in data["chan_models"].values()) - len(data["orphan_notes"])
    expected = {
        "users": (src_counts["users"], ""),
        "api_keys": (src_counts["api_keys"], ""),
        "llm_infos": (src_counts["llm_infos"], "丢弃 context_length/max_output_tokens"),
        "channels": (src_counts["channels"], "取唯一 URL;dialect=generic;统计置 0"),
        "channel_keys": (src_counts["channel_keys"], "channel_key→key;name=default;统计置 0"),
        "channel_models": (len(data["models_rows"]),
                           f"拆分 {split_base} + 孤儿补入 {len(data['orphan_notes'])}"),
        "channel_grants": (len(data["grants_rows"]), "模型×凭据,protocols=渠道协议位"),
        "groups": (src_counts["groups"], f"转换 {src_counts['groups']}"),
        "group_items": (src_counts["group_items"], f"转换 {src_counts['group_items']}"),
    }

    mismatches = []
    print("=" * 78)
    print("fork → v2 转换对账报告")
    print("=" * 78)
    print(f"{'表':<16}{'源行数':>10}{'目标行数':>10}  状态")
    print("-" * 78)
    for t, (exp, note) in expected.items():
        dst_n = table_count(dst, t)
        ok = dst_n == exp
        if not ok:
            mismatches.append(t)
        mark = "OK" if ok else "不一致!"
        print(f"{t:<16}{exp:>10}{dst_n:>10}  {mark} {note}")

    # settings 单列一行(合并语义,不做数量对账)
    carried = data["settings_carried"]
    print(f"{'settings':<16}{len(data['fork_settings']):>10}{table_count(dst, 'settings'):>10}"
          f"  仅交集 {len(carried)} 键覆盖: {', '.join(carried)}")

    for t, src_n, dst_n, status, dropped in data["conditional_status"]:
        note = f"列丢弃 {dropped}" if dropped else ""
        print(f"{t:<16}{src_n:>10}{str(dst_n):>10}  {status} {note}")

    print(f"{'stats 8 张':<16}{'-':>10}{'-':>10}  不带(ADR-0005,统计累计清零重新攒)")
    print(f"{'migration_records':<16}{'-':>10}{table_count(dst, 'migration_records'):>10}"
          f"  保留目标模板自身(不重跑迁移)")

    print("-" * 78)
    # 渠道协议位分布
    proto_dist = {}
    for cid, p in data["chan_protocol"].items():
        proto_dist[p] = proto_dist.get(p, 0) + 1
    dist_str = ", ".join(
        f"位{p}({'openai-chat' if p == 2 else 'openai-response' if p == 4 else 'anthropic'}): {n} 渠道"
        for p, n in sorted(proto_dist.items()))
    print(f"渠道协议位分布: {dist_str}")
    print(f"base_url 归一化(去结尾 /v1,防 /v1/v1 双叠): {data['normalize_count']} 渠道")

    # 孤儿模型补入清单
    if data["orphan_notes"]:
        print("孤儿模型补入(group_items 引用但渠道清单缺失,已追加进 channel_models):")
        for gi_id, gid, cid, mname in data["orphan_notes"]:
            print(f"  - group_item {gi_id}(组 {gid}) → 渠道 {cid} 补入模型 {mname!r}")
    else:
        print("孤儿模型补入: 无")

    # 轮询降级分组清单(D1)
    if data["degraded_poll"]:
        names = ", ".join(f"{gid}:{name}" for gid, name in data["degraded_poll"])
        print(f"轮询降级分组(D1→failover,丢负载分摊): {names}")
    else:
        print("轮询降级分组: 无")

    # D2 请求模型名扫描(仅报告,不建别名组 —— 2026-09-08 用户终裁)
    if data["regex_scan"]:
        print("relay_logs 请求模型名扫描(D2,仅报告):")
        for name, total, failed, decision in data["regex_scan"]:
            print(f"  - {name!r}: {total} 次请求(失败 {failed}), {decision}")
    else:
        print("relay_logs 请求模型名扫描: 源库无 relay_logs,跳过")

    # 丢弃字段汇总
    print("丢弃字段汇总:")
    for line in DROPPED_FIELDS_SUMMARY:
        print(f"  - {line}")
    print("=" * 78)

    dst.close()
    return mismatches


def main():
    parser = argparse.ArgumentParser(
        description="fork(v1.x) → dev-v2(v0.13.2 schema) 数据库转换脚本(ADR-0005)。"
                    "源库与模板库只读,目标库先删后建,失败自动回滚清理。")
    parser.add_argument("--src", required=True, help="fork 源库路径(只读打开)")
    parser.add_argument("--dst", required=True, help="转换产物库路径(先删后建)")
    parser.add_argument("--template", required=True,
                        help="v2 模板库路径(迁移链跑完的空库,只读复制)")
    args = parser.parse_args()

    src, dst, template = (Path(p).absolute() for p in (args.src, args.dst, args.template))
    if not src.exists():
        print(f"错误: 源库不存在: {src}", file=sys.stderr)
        return 2
    if not template.exists():
        print(f"错误: 模板库不存在: {template}", file=sys.stderr)
        return 2
    if len({src, dst, template}) != 3:
        print("错误: --src/--dst/--template 不得指向同一路径", file=sys.stderr)
        return 2

    try:
        data = convert(src, dst, template)
    except ConvertError as e:
        print(f"错误: {e}(已回滚并删除 {dst})", file=sys.stderr)
        return 2
    except sqlite3.DatabaseError as e:
        print(f"错误: 源库或模板库无法读取,不是有效的 SQLite 库: {e}", file=sys.stderr)
        return 2

    mismatches = print_report(data, dst)
    if mismatches:
        print(f"对账不一致: {', '.join(mismatches)}", file=sys.stderr)
        return 1
    print(f"转换完成: {dst}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
