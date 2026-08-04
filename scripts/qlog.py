#!/usr/bin/env python3
# octopus 日志查询脚本 —— 供本机 AI agent / 排查工具调用
#
# 用途:结构化输出 relay_logs,排查 504/524/熔断/content-vs-reasoning 等问题。
# 数据源:Octopus 的 SQLite 数据库 (默认 data/data.db),并发只读,不会写库。
#
# 用法(常见):
#   python3 scripts/qlog.py                      # 最近 20 条
#   python3 scripts/qlog.py -n 50                 # 最近 50 条
#   python3 scripts/qlog.py --error              # 只看有错误的
#   python3 scripts/qlog.py --error --model deepseek-v4-flash
#   python3 scripts/qlog.py --since 1h           # 最近1小时
#   python3 scripts/qlog.py --since 2026-08-03
#   python3 scripts/qlog.py --circuit            # 包含熔断跳过的请求
#   python3 scripts/qlog.py --id 1785729296406 --full   # 单条全字段(含 prompt/response)
#   python3 scripts/qlog.py --full               # 每条带 attempt 链 + error + response 摘要
#   python3 scripts/qlog.py --json               # 原始 JSON 数组输出(便于 AI 解析)
#
# 输出格式:默认是人类可读的精简摘要;--json 切换为机器可读 JSON 数组。
# --full 才带 request_content/response_content/debug_content(大字段,默认省略以降噪省 token)。

import argparse
import json
import os
import sqlite3
import sys
import time

DEFAULT_DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data", "data.db")


def parse_since(s):
    """支持 --since 1h / 30m / 2d / YYYY-MM-DD / YYYY-MM-DD HH:MM"""
    s = s.strip()
    mult = {"s": 1, "m": 60, "h": 3600, "d": 86400}
    if s and s[-1] in mult:
        try:
            return int(time.time()) - int(s[:-1]) * mult[s[-1]]
        except ValueError:
            pass
    for fmt in ("%Y-%m-%d %H:%M", "%Y-%m-%d"):
        try:
            return int(time.mktime(time.strptime(s, fmt)))
        except ValueError:
            continue
    raise SystemExit(f"无法解析 --since 的值: {s!r}")


def at_summary(attempts):
    """把 attempts 数组压缩成可读的尝试链"""
    if not attempts:
        return []
    out = []
    for a in attempts:
        out.append({
            "n": a.get("attempt_num"),
            "status": a.get("status"),
            "channel": a.get("channel_name"),
            "key": a.get("channel_key_remark"),
            "model": a.get("model_name"),
            "dur_ms": a.get("duration"),
            "msg": a.get("msg"),
            "sticky": a.get("sticky"),
        })
    return out


def resp_summary(response_content, max_len=2000):
    """从 response_content 里提取 content / reasoning_content 的长度与片段,用于判断"答案跑到 reasoning"这类问题"""
    s = response_content or ""
    if not s:
        return None
    snippet = s[:max_len]
    info = {"len": len(s), "head": snippet}
    try:
        rj = json.loads(s)
        chs = rj.get("choices") or []
        if chs:
            m = chs[0].get("message") or {}
            c = m.get("content") or ""
            rc = m.get("reasoning_content") or m.get("reasoning") or ""
            fr = chs[0].get("finish_reason")
            info["content_len"] = len(c)
            info["reasoning_len"] = len(rc)
            info["finish_reason"] = fr
            if c:
                info["content_head"] = c[:200]
            if rc:
                info["reasoning_head"] = rc[:200]
    except Exception:
        info["parse"] = "non_json_or_sse"
    return info


def build_query(args):
    where = []
    params = {}
    if args.error:
        where.append("error IS NOT NULL AND TRIM(error) != ''")
    if args.model:
        where.append("request_model_name = :model")
        params["model"] = args.model
    if args.api_key:
        where.append("request_api_key_name = :api_key")
        params["api_key"] = args.api_key
    if args.channel:
        where.append("channel_name LIKE :channel")
        params["channel"] = f"%{args.channel}%"
    if args.since:
        where.append("time >= :since")
        params["since"] = parse_since(args.since)
    if args.until:
        where.append("time <= :until")
        params["until"] = parse_since(args.until)
    return where, params


def main():
    ap = argparse.ArgumentParser(description="Octopus relay log 查询 (供 AI/排查用)")
    ap.add_argument("-n", "--limit", type=int, default=20, help="返回条数 (默认 20)")
    ap.add_argument("--error", action="store_true", help="只返回有 error 的")
    ap.add_argument("--model", help="按请求模型名过滤")
    ap.add_argument("--api-key", dest="api_key", help="按 api key 名过滤")
    ap.add_argument("--channel", help="按 (成功)渠道名模糊过滤")
    ap.add_argument("--since", help="起始时间: 1h/30m/2d 或 YYYY-MM-DD[ HH:MM]")
    ap.add_argument("--until", help="结束时间: 同上")
    ap.add_argument("--id", type=int, help="查单条日志 id (与过滤互斥)")
    ap.add_argument("--circuit", action="store_true", help="只返回 attempts 含 circuit_break 的")
    ap.add_argument("--by-channel", dest="by_channel", help="按渠道展开 attempts:每个尝试一行,限定到指定渠道名(支持子串模糊匹配)。例如 --by-channel 'Maoyulin'")
    ap.add_argument("--full", action="store_true", help="带 request_content/response_content/debug_content")
    ap.add_argument("--json", action="store_true", help="输出 JSON 数组 (机器可读)")
    ap.add_argument("--db", default=DEFAULT_DB, help=f"数据库路径 (默认 {DEFAULT_DB})")
    args = ap.parse_args()

    if not os.path.exists(args.db):
        raise SystemExit(f"数据库不存在: {args.db}")

    # URI + 只读模式,安全(不会写库,且支持并发读)
    conn = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True, timeout=5)
    conn.row_factory = sqlite3.Row

    base_cols = (
        "id, time, request_model_name, request_api_key_name, channel_name, "
        "actual_model_name, reasoning_effort, input_tokens, output_tokens, "
        "cached_tokens, cache_creation_tokens, ftut, use_time, cost, "
        "error, attempts, total_attempts, client_name, user_agent"
    )
    full_cols = base_cols + ", request_content, response_content, debug_content"

    if args.by_channel:
        # —— 按渠道展开视图 ——
        # 取出所有请求(只取含 attempts 的字段,够展开明细用),再展开 attempts 为每尝试一行。
        # 限定到指定渠道(子串模糊匹配)。带 --since/--error 等过滤一起作用于"所在请求"层级。
        # 注意:attempts 是 JSON 字段存在 relay_logs 里,无法在 SQL 层展开,这里全量拉回内存展开。
        where, params = build_query(args)
        sql = f"SELECT {base_cols} FROM relay_logs"
        if where:
            sql += " WHERE " + " AND ".join(where)
        sql += " ORDER BY id DESC LIMIT :scan"
        # 扫描窗口: 扩大默认上限以保证目标渠道有足够样本,
        # 否则 LIMIT 太小反而抓不到目标渠道出现在靠后 attempt 里的请求。
        params["scan"] = max(args.limit * 20, 500, args.limit)
        all_rows = list(conn.execute(sql, params))
        conn.close()

        needle = args.by_channel
        expanded = []
        for r in all_rows:
            aid = r["id"]
            ts = r["time"]
            rmodel = r["request_model_name"]
            rerr = r["error"] or ""
            raw_att = r["attempts"] or ""
            atts = []
            if raw_att:
                try:
                    atts = json.loads(raw_att)
                except Exception:
                    continue
            for a in atts:
                ch = a.get("channel_name") or ""
                if needle.lower() not in ch.lower():
                    continue
                expanded.append({
                    "req_id": aid,
                    "req_time": ts,
                    "request_model": rmodel,
                    "req_error": rerr,
                    "attempt_num": a.get("attempt_num"),
                    "status": a.get("status"),
                    "channel": ch,
                    "channel_key": a.get("channel_key_remark"),
                    "model": a.get("model_name"),
                    "dur_ms": a.get("duration"),
                    "sticky": a.get("sticky"),
                    "msg": a.get("msg"),
                })

        if args.json:
            print(json.dumps(expanded, ensure_ascii=False, indent=2, default=str))
            return
        if not expanded:
            print(f"(无匹配渠道[{needle}]的尝试记录)")
            return
        print(f"# 渠道匹配 [{needle}] —— 共 {len(expanded)} 次尝试 (扫描 {len(all_rows)} 个请求)")
        # 简单统计
        from collections import Counter
        st = Counter(e["status"] for e in expanded)
        print(f"   状态分布: {dict(st)}")
        print()
        for e in expanded:
            ts_s = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(e["req_time"]))
            flag = "  (req-ERROR)" if e["req_error"] else ""
            print(f"#{e['req_id']} {ts_s} {e['request_model']}{flag}")
            print(f"   attempt#{e['attempt_num']} [{e['status']}] {e['channel']}"
                  f"{('/'+str(e['model'])) if e['model'] else ''} "
                  f"{e['dur_ms']!s}ms{(' sticky') if e['sticky'] else ''}")
            if e["msg"]:
                print(f"     -> {e['msg'][:200]}")
        return

    if args.id:
        cols = full_cols if args.full else base_cols
        rows = list(conn.execute(f"SELECT {cols} FROM relay_logs WHERE id = ?", (args.id,)))
    else:
        where, params = build_query(args)
        cols = full_cols if (args.full or args.json) else base_cols
        sql = f"SELECT {cols} FROM relay_logs"
        if where:
            sql += " WHERE " + " AND ".join(where)
        sql += " ORDER BY id DESC LIMIT :limit"
        params["limit"] = args.limit
        rows = list(conn.execute(sql, params))
        if args.circuit:
            rows = [r for r in rows if (r["attempts"] or "").find("circuit_break") >= 0]

    conn.close()

    out = []
    for r in rows:
        d = {k: r[k] for k in r.keys()}
        if d.get("attempts"):
            try:
                d["attempts_parsed"] = at_summary(json.loads(d["attempts"]))
            except Exception:
                d["attempts_parsed"] = d["attempts"]
        if args.full and d.get("response_content"):
            d["response_summary"] = resp_summary(d["response_content"])
        out.append(d)

    if args.json:
        print(json.dumps(out, ensure_ascii=False, indent=2, default=str))
        return

    # 人类可读精简输出
    if not out:
        print("(无匹配日志)")
        return
    for d in out:
        ts = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(d["time"]))
        errflag = "  ERROR" if d.get("error") else ""
        print(f"#{d['id']} {ts} {d['request_model_name']}{errflag}")
        print(f"   channel={d['channel_name']} actual={d['actual_model_name']} "
              f"attempts={d['total_attempts']} ftut={d['ftut']}ms use={d['use_time']}ms")
        print(f"   tok in={d['input_tokens']} out={d['output_tokens']} cached={d['cached_tokens']} cost={d['cost']}")
        ap_list = d.get("attempts_parsed")
        if isinstance(ap_list, list) and ap_list:
            chain = []
            for a in ap_list:
                tag = a["status"]
                if a.get("sticky"):
                    tag += "(sticky)"
                chain.append(f"[{a['n']}{tag}:{a['channel']}/{a['model']} {a['dur_ms']}ms]")
            print("   " + " -> ".join(chain))
        if d.get("error"):
            print(f"   err: {d['error'][:240]}")
        if args.full and d.get("response_summary"):
            print(f"   resp: {json.dumps(d['response_summary'], ensure_ascii=False)[:240]}")
        print()


if __name__ == "__main__":
    main()
