#!/usr/bin/env python3
"""A2 渠道导出:从 dev 009 schema 库导出渠道,降级多URL/多KEY → 单 BaseURL/单Key,
供 feat(上游 006 单 BaseURL/单 Key 结构)再导入。

设计依据: .trellis/tasks/08-19-octopus-route-a-migration/design.md §3 A2。
映射来源(均 verified):
  - dev 渠道表 base_urls JSON: [{url, delay, type(int), provider_id}]
  - dev channel_keys 表: 真实 key 在 channel_key 列(每渠道可多 key)
  - dev OutboundType int iota: internal/transformer/outbound/register.go
  - feat ChannelProvider string: internal/model/channel.go

用法: python3 scripts/export_channels.py <dev_db> <output_json>
只读打开(ro+immutable),不锁线上库。
"""
import json
import sqlite3
import sys

# dev OutboundType(int) → feat ChannelProvider(string)
TYPE_MAP = {
    0: "openai",             # OutboundTypeOpenAIChat
    1: "openai_responses",   # OutboundTypeOpenAIResponse
    2: "anthropic",          # OutboundTypeAnthropic
    3: "gemini",             # OutboundTypeGemini
    4: "volcengine",         # OutboundTypeVolcengine
    5: "openai",             # OutboundTypeOpenAIEmbedding(feat 无 embedding,降级 openai)
}


def main():
    if len(sys.argv) != 3:
        print("usage: export_channels.py <dev_db> <output_json>", file=sys.stderr)
        sys.exit(1)
    dev_db, out_json = sys.argv[1], sys.argv[2]

    # ro + immutable: 视文件为只读快照,不拿写锁,不影响线上容器
    con = sqlite3.connect(f"file:{dev_db}?mode=ro&immutable=1", uri=True)
    con.row_factory = sqlite3.Row

    # keys 按 channel_id 聚合
    keys_by_chan = {}
    for r in con.execute("SELECT channel_id, channel_key, enabled FROM channel_keys"):
        keys_by_chan.setdefault(r["channel_id"], []).append(
            {"key": r["channel_key"], "enabled": bool(r["enabled"])})

    out = []
    seen_names = {}
    skipped = {"no_base": 0, "no_key": 0}
    src_count = 0
    for c in con.execute("SELECT * FROM channels"):
        src_count += 1
        cid = c["id"]
        raw = c["base_urls"]
        base_urls = []
        if raw and raw.strip() not in ("", "[]", "null"):
            try:
                base_urls = json.loads(raw)
            except json.JSONDecodeError as e:
                print(f"[warn] channel id={cid} base_urls 解析失败: {e}", file=sys.stderr)
        base_urls = [b for b in base_urls if isinstance(b, dict) and b.get("url")]
        if not base_urls:
            skipped["no_base"] += 1
            continue
        keys = [k for k in keys_by_chan.get(cid, []) if k["key"]]
        if not keys:
            skipped["no_key"] += 1
            continue

        multi = (len(base_urls) > 1) or (len(keys) > 1)
        for bi, b in enumerate(base_urls):
            t = TYPE_MAP.get(b.get("type"), "openai")
            for kj, k in enumerate(keys):
                name = c["name"]
                if multi:
                    name = f"{c['name']} [b{bi+1} k{kj+1}]"
                # name unique 兜底去重
                if name in seen_names:
                    seen_names[name] += 1
                    name = f"{name} #{seen_names[name]}"
                else:
                    seen_names[name] = 1

                ch = {
                    "name": name,
                    "type": t,
                    "enabled": bool(c["enabled"]) and not bool(c["auto_disabled"]),
                    "base_url": b["url"],
                    "key": k["key"],
                    "model": c["model"] or "",
                    "custom_model": c["custom_model"] or "",
                    "proxy": bool(c["proxy"]),
                    "auto_sync": False,  # 导入不触发自动模型同步风暴,稳一点
                }
                # feat 这几个字段是 *string,直接透传字符串
                if c["param_override"]:
                    ch["param_override"] = c["param_override"]
                if c["channel_proxy"]:
                    ch["channel_proxy"] = c["channel_proxy"]
                if c["match_regex"]:
                    ch["match_regex"] = c["match_regex"]
                if c["custom_header"]:
                    try:
                        ch["custom_header"] = json.loads(c["custom_header"])
                    except json.JSONDecodeError:
                        pass
                out.append(ch)

    con.close()
    with open(out_json, "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, indent=2)
    print(f"导出 {len(out)} 个 feat 渠道(来自 {src_count} 个 dev 渠道),"
          f"跳过 no_base={skipped['no_base']} no_key={skipped['no_key']} -> {out_json}")


if __name__ == "__main__":
    main()
