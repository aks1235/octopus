#!/usr/bin/env python3
"""A2 渠道导入:登录 feat 容器(8081,admin/admin)拿 auth cookie,
批量 POST /api/v1/channel/create 导入 export_channels.py 产出的 JSON。

设计依据: .trellis/tasks/08-19-octopus-route-a-migration/implement.md Step 8.3。
用法: python3 scripts/import_channels.py <base_url> <channels_json>
示例: python3 scripts/import_channels.py http://127.0.0.1:8081 data/channels-export.json
"""
import json
import sys
import urllib.error
import urllib.request

BASE = sys.argv[1] if len(sys.argv) > 2 else "http://127.0.0.1:8081"
SRC = sys.argv[2] if len(sys.argv) > 2 else "data/channels-export.json"
if len(sys.argv) == 1:
    BASE, SRC = "http://127.0.0.1:8081", "data/channels-export.json"
elif len(sys.argv) == 2:
    print("usage: import_channels.py <base_url> <channels_json>", file=sys.stderr)
    sys.exit(1)


def req(path, body=None, cookie=None):
    headers = {"Content-Type": "application/json"}
    if cookie:
        headers["Cookie"] = cookie
    data = json.dumps(body).encode() if body else None
    r = urllib.request.Request(f"{BASE}{path}", data=data, headers=headers)
    with urllib.request.urlopen(r, timeout=60) as resp:
        return resp.status, resp.headers, resp.read().decode()


# 登录,拿 Set-Cookie
_, hdrs, _ = req("/api/v1/user/login", {"username": "admin", "password": "admin"})
cookie = hdrs.get("Set-Cookie", "").split(";")[0]
if not cookie.startswith("auth="):
    print("[fatal] login 未拿到 auth cookie", file=sys.stderr)
    sys.exit(1)
print(f"login OK, cookie={cookie[:20]}...")

with open(SRC, encoding="utf-8") as f:
    channels = json.load(f)
print(f"待导入 {len(channels)} 个渠道")

ok, fail, errors = 0, 0, []
for i, ch in enumerate(channels, 1):
    name = ch.get("name", "?")
    try:
        status, _, body = req("/api/v1/channel/create", ch, cookie)
        if status == 200:
            ok += 1
            if i % 10 == 0:
                print(f"  [{i}/{len(channels)}] OK (累计 ok={ok} fail={fail})")
        else:
            fail += 1
            errors.append((name, body[:120]))
    except urllib.error.HTTPError as e:
        fail += 1
        errors.append((name, f"HTTP {e.code} {e.read().decode()[:120]}"))
    except Exception as e:
        fail += 1
        errors.append((name, repr(e)[:120]))

print(f"\n=== 导入完成: ok={ok} fail={fail} total={len(channels)} ===")
for name, err in errors[:20]:
    print(f"  FAIL {name!r}: {err}")
if len(errors) > 20:
    print(f"  ...(另有 {len(errors)-20} 条失败)")

# 复查
status, _, body = req("/api/v1/channel/list", None, cookie)
data = json.loads(body).get("data", [])
print(f"库内现有渠道数: {len(data)}")
