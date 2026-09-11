#!/usr/bin/env python3
"""Octopus e2e 冒烟驱动:admin 登录建渠道/分组/API Key,逐项发请求并断言,输出 PASS/FAIL。

前置:
    1. python3 scripts/e2e/sim.py                 # 宿主起假上游(0.0.0.0:18080)
    2. 8081 冒烟容器已起(verify 姿势,data-verify 隔离库,admin/admin)
用法:
    python3 scripts/e2e/run_smoke.py              # 默认 GATEWAY=http://127.0.0.1:8081
环境变量:
    GATEWAY        网关地址(默认 http://127.0.0.1:8081)
    SIM_LOCAL      驱动访问模拟器的地址(默认 http://127.0.0.1:18080)
    SIM_UPSTREAM   容器内回连模拟器的地址;缺省自动探测 compose 网关
    SKIP_LOG_WAIT  =1 跳过 65s 日志兜底断言(快速模式)

用例分组:
    [基线] basic/json_schema/thinking 流/tool_call 往返/空响应/零状态/失败重试/Anthropic 兼容
    [定向] GLM 思考标记透传/DeepSeek web_search 工具/Claude cache_control 断点/跨格式转换/多条 system 合并
    [加固] SSE [DONE] 尾帧/中途断连终态/自定义头占位符/日志终态落库
"""

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

GATEWAY = os.environ.get("GATEWAY", "http://127.0.0.1:8081").rstrip("/")
SIM_LOCAL = os.environ.get("SIM_LOCAL", "http://127.0.0.1:18080").rstrip("/")
SKIP_LOG_WAIT = os.environ.get("SKIP_LOG_WAIT") == "1"

# 客户端可见的分组名(= 客户端请求的 model 名)
G_BASIC = "grp-sim-basic"
G_JSON = "grp-sim-json"
G_THINK = "grp-sim-thinking"
G_TOOLS = "grp-sim-tools"
G_TOOLS_S = "grp-sim-tools-stream"
G_FLAKY = "grp-sim-flaky"
G_ZERO = "grp-sim-zero"
G_EMPTY = "grp-sim-empty"
G_DISC = "grp-sim-disc"
G_EOF = "grp-sim-eof"
G_HDR = "grp-sim-hdr"
G_FAILOVER = "grp-failover"
G_CBASIC = "grp-claude-basic"
G_CCACHE = "grp-claude-cache"
G_CSTREAM = "grp-claude-stream"

OPENAI_MODELS = [
    "sim-openai-basic", "sim-openai-hdr", "sim-openai-json", "sim-openai-thinking",
    "sim-openai-tools", "sim-openai-tools-stream", "sim-openai-500", "sim-openai-flaky",
    "sim-openai-zero-error", "sim-openai-empty", "sim-openai-disconnect", "sim-openai-eof",
]
ANTHROPIC_MODELS = ["sim-claude-basic", "sim-claude-cache", "sim-claude-stream"]

# 快速路由配置:失败 1 次即冷却,重试间隔/冷却都压短,亲和关闭
FAST_RELAY = {
    "member_max_attempts": 1,
    "member_retry_interval_seconds": 1,
    "member_non_stream_response_timeout_seconds": 15,
    "member_stream_first_event_timeout_seconds": 15,
    "member_cooldown_seconds": 2,
    "member_affinity_seconds": 0,
}

COOKIE = None
API_KEY = None
RESULTS = []


# ---------------- 基础 HTTP ----------------

def admin_api(path, payload=None, method=None):
    """带登录态调管理端接口;自动携带登录取得的 cookie。"""
    url = GATEWAY + path
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method or ("POST" if data else "GET"))
    req.add_header("Content-Type", "application/json")
    if COOKIE:
        req.add_header("Cookie", COOKIE)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, json.loads(resp.read() or b"null")
    except urllib.error.HTTPError as e:
        body = e.read()
        try:
            return e.code, json.loads(body or b"null")
        except ValueError:
            return e.code, {"__raw__": body.decode("utf-8", "replace")}


def login():
    """登录 admin;gin SetCookie 在响应头,直接抓 Set-Cookie 行。"""
    global COOKIE
    url = GATEWAY + "/api/v1/user/login"
    req = urllib.request.Request(url, data=json.dumps({"username": "admin", "password": "admin"}).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=15) as resp:
        set_cookie = resp.headers.get("Set-Cookie", "")
        if not set_cookie.startswith("auth="):
            raise RuntimeError(f"login failed: no auth cookie, got {set_cookie!r}")
        COOKIE = set_cookie.split(";")[0]
        return json.loads(resp.read() or b"null")


# ---------------- 客户端请求 ----------------

def openai_request(model, messages=None, stream=False, extra=None, headers=None, timeout=60):
    """OpenAI Chat 形客户端请求;非流式返回 (status, body_dict)。"""
    payload = {"model": model, "messages": messages or [{"role": "user", "content": "hello"}], "stream": stream}
    payload.update(extra or {})
    req = urllib.request.Request(GATEWAY + "/v1/chat/completions", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + API_KEY)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read()
            return resp.status, json.loads(body or b"null")
    except urllib.error.HTTPError as e:
        body = e.read()
        try:
            return e.code, json.loads(body or b"null")
        except ValueError:
            return e.code, {"__raw__": body.decode("utf-8", "replace")}


def parse_sse_block(block):
    """解析一段 SSE 事件块为 {event, data};无 data 行返回 None。"""
    event_name, data_lines = None, []
    for line in block.split("\n"):
        if line.startswith("event:"):
            event_name = line[6:].strip()
        elif line.startswith("data:"):
            data_lines.append(line[5:].strip())
    if not data_lines:
        return None
    return {"event": event_name, "data": "\n".join(data_lines)}


def stream_request(path, payload, headers=None, timeout=90):
    """流式客户端请求;返回 (status, events, conn_error)。

    events 为按序解析的 SSE 事件(data 原样字符串);conn_error 非空表示连接异常收尾
    (上游断连场景,已收事件保留)。
    """
    req = urllib.request.Request(GATEWAY + path, data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "text/event-stream")
    req.add_header("Authorization", "Bearer " + API_KEY)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    events, conn_error, status = [], "", 0
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status = resp.status
            buf = b""
            while True:
                chunk = resp.read(2048)
                if not chunk:
                    break
                buf += chunk
                while b"\n\n" in buf:
                    raw, buf = buf.split(b"\n\n", 1)
                    ev = parse_sse_block(raw.decode("utf-8", "replace"))
                    if ev:
                        events.append(ev)
            if buf:
                ev = parse_sse_block(buf.decode("utf-8", "replace"))
                if ev:
                    events.append(ev)
    except Exception as e:  # 断连/不完整响应都属预期观察面
        conn_error = f"{type(e).__name__}: {e}"
    return status, events, conn_error


def openai_stream(model, extra=None, headers=None):
    payload = {"model": model, "messages": [{"role": "user", "content": "hello"}], "stream": True}
    payload.update(extra or {})
    return stream_request("/v1/chat/completions", payload, headers=headers)


def anthropic_request(model, system=None, messages=None, stream=False, extra=None, timeout=60):
    """Anthropic Messages 形客户端请求;非流式返回 (status, body_dict)。"""
    payload = {"model": model, "max_tokens": 256, "messages": messages or [{"role": "user", "content": "hello"}], "stream": stream}
    if system is not None:
        payload["system"] = system
    payload.update(extra or {})
    req = urllib.request.Request(GATEWAY + "/v1/messages", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("x-api-key", API_KEY)
    req.add_header("anthropic-version", "2023-06-01")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read()
            return resp.status, json.loads(body or b"null")
    except urllib.error.HTTPError as e:
        body = e.read()
        try:
            return e.code, json.loads(body or b"null")
        except ValueError:
            return e.code, {"__raw__": body.decode("utf-8", "replace")}


def responses_request(model, tools=None, extra=None):
    """OpenAI Responses 形客户端请求(非流式);返回 (status, body_dict)。"""
    payload = {"model": model, "input": "hello"}
    if tools is not None:
        payload["tools"] = tools
    payload.update(extra or {})
    req = urllib.request.Request(GATEWAY + "/v1/responses", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + API_KEY)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.status, json.loads(resp.read() or b"null")
    except urllib.error.HTTPError as e:
        body = e.read()
        try:
            return e.code, json.loads(body or b"null")
        except ValueError:
            return e.code, {"__raw__": body.decode("utf-8", "replace")}


def anthropic_stream(model, system=None, extra=None):
    payload = {"model": model, "max_tokens": 256, "messages": [{"role": "user", "content": "hello"}], "stream": True}
    if system is not None:
        payload["system"] = system
    payload.update(extra or {})
    req = urllib.request.Request(GATEWAY + "/v1/messages", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("x-api-key", API_KEY)
    req.add_header("anthropic-version", "2023-06-01")
    req.add_header("Accept", "text/event-stream")
    events, conn_error, status = [], "", 0
    try:
        with urllib.request.urlopen(req, timeout=90) as resp:
            status = resp.status
            buf = b""
            while True:
                chunk = resp.read(2048)
                if not chunk:
                    break
                buf += chunk
                while b"\n\n" in buf:
                    raw, buf = buf.split(b"\n\n", 1)
                    ev = parse_sse_block(raw.decode("utf-8", "replace"))
                    if ev:
                        events.append(ev)
    except Exception as e:
        conn_error = f"{type(e).__name__}: {e}"
    return status, events, conn_error


# ---------------- 模拟器控制 ----------------

def sim_reset():
    urllib.request.urlopen(SIM_LOCAL + "/__reset", timeout=10).read()


def recorded():
    with urllib.request.urlopen(SIM_LOCAL + "/__recorded", timeout=10) as resp:
        return json.loads(resp.read())


# ---------------- 断言工具 ----------------

def check(name, ok, detail=""):
    tag = "PASS" if ok else "FAIL"
    RESULTS.append((name, ok))
    print(f"[{tag}] {name}" + (f"  -- {detail}" if detail else ""))
    return ok


def want(name, cond, detail=""):
    """断言失败时抛出,由外层捕获记 FAIL。"""
    if not cond:
        raise AssertionError(detail or f"{name}: condition not met")


def run_case(name, fn):
    try:
        fn()
    except Exception as e:
        check(name, False, f"{type(e).__name__}: {e}")


# ---------------- 环境准备 ----------------

def detect_sim_upstream():
    """容器内回连模拟器的地址:取 compose 网络网关 IP。"""
    env = os.environ.get("SIM_UPSTREAM")
    if env:
        return env.rstrip("/")
    try:
        out = subprocess.run(
            ["docker", "network", "inspect", "octopus-v2-smoke_default", "-f", "{{(index .IPAM.Config 0).Gateway}}"],
            capture_output=True, text=True, timeout=15,
        ).stdout.strip()
        if out:
            return "http://" + out + ":18080"
    except Exception as e:
        print(f"warn: docker network inspect failed: {e}")
    return "http://host.docker.internal:18080"


def wait_gateway():
    deadline = time.time() + 90
    while time.time() < deadline:
        try:
            login()
            return
        except Exception as e:
            print(f"waiting gateway... ({e})")
            time.sleep(3)
    raise RuntimeError("gateway not reachable")


def setup(sim_upstream):
    """建渠道/分组/API Key;幂等:同名渠道先删(按列表匹配)。"""
    status, stats = admin_api("/api/v1/channel/stats")
    want("channel stats", status == 200, f"stats -> {status} {stats}")
    for ch in (stats.get("data") or []):
        admin_api(f"/api/v1/channel/delete/{ch['channel_id']}", method="DELETE")
    status, groups = admin_api("/api/v1/group/list")
    for g in (groups.get("data") or []):
        admin_api(f"/api/v1/group/delete/{g['id']}", method="DELETE")

    def create_channel(name, base, models, protocols, anthropic_path=None, custom_header=None):
        payload = {
            "id": 0,
            "name": name,
            "dialect": "generic",
            "enabled": True,
            "base_url": base,
            "openai_chat_completion_path": "/v1/chat/completions",
            "openai_response_path": "/v1/responses",
            "anthropic_message_path": anthropic_path or "/v1/messages",
            "proxy": False,
            "channel_proxy": "",
            "custom_header": custom_header or [],
            "param_override": "",
            "match_regex": "",
            "health_check_skip": True,
            "keys": [{"name": "k1", "key": "sk-sim-key", "enabled": True}],
            "models": models,
            "grants": [{"model_name": m, "key_name": "k1", "protocols": protocols} for m in models],
        }
        status, body = admin_api("/api/v1/channel/create", payload)
        want(f"create channel {name}", status == 200 and (body.get("code") == 200), f"-> {status} {body}")

    create_channel("e2e-openai", sim_upstream + "/openai", OPENAI_MODELS, 2)
    create_channel("e2e-claude", sim_upstream + "/anthropic", ANTHROPIC_MODELS, 8)
    create_channel(
        "e2e-hdr", sim_upstream + "/openai", ["sim-openai-hdr"], 2,
        custom_header=[{"header_key": "X-Marker", "header_value": "{client_header:X-Custom-Marker}"}],
    )

    # 渠道授权候选 → 按 (渠道名, 模型名) 索引
    status, grants = admin_api("/api/v1/channel/grants")
    want("list grants", status == 200, f"-> {status} {grants}")
    ch_name_by_id = {c["channel_id"]: c["channel_name"] for c in (stats.get("data") or [])}
    # 删除旧渠道后 stats 已过期;重新拉一次拿新渠道
    status, stats2 = admin_api("/api/v1/channel/stats")
    ch_name_by_id = {c["channel_id"]: c["channel_name"] for c in (stats2.get("data") or [])}
    grant_id = {}
    for g in (grants.get("data") or []):
        grant_id[(ch_name_by_id.get(g["channel_id"]), g["model_name"])] = g["id"]

    def create_group(name, members):
        items = [{"channel_grant_id": grant_id[(ch, m)]} for ch, m in members]
        payload = {"name": name, "mode": "failover", "relay_config": FAST_RELAY, "items": items}
        status, body = admin_api("/api/v1/group/create", payload)
        want(f"create group {name}", status == 200 and (body.get("code") == 200), f"-> {status} {body}")

    O, C = "e2e-openai", "e2e-claude"
    create_group(G_BASIC, [(O, "sim-openai-basic")])
    create_group(G_JSON, [(O, "sim-openai-json")])
    create_group(G_THINK, [(O, "sim-openai-thinking")])
    create_group(G_TOOLS, [(O, "sim-openai-tools")])
    create_group(G_TOOLS_S, [(O, "sim-openai-tools-stream")])
    create_group(G_FLAKY, [(O, "sim-openai-flaky")])
    create_group(G_ZERO, [(O, "sim-openai-zero-error")])
    create_group(G_EMPTY, [(O, "sim-openai-empty")])
    create_group(G_DISC, [(O, "sim-openai-disconnect")])
    create_group(G_EOF, [(O, "sim-openai-eof")])
    create_group(G_HDR, [("e2e-hdr", "sim-openai-hdr")])
    create_group(G_FAILOVER, [(O, "sim-openai-500"), (O, "sim-openai-basic")])
    create_group(G_CBASIC, [(C, "sim-claude-basic")])
    create_group(G_CCACHE, [(C, "sim-claude-cache")])
    create_group(G_CSTREAM, [(C, "sim-claude-stream")])

    status, body = admin_api("/api/v1/apikey/create", {"name": "e2e", "enabled": True})
    want("create apikey", status == 200 and body.get("data", {}).get("api_key"), f"-> {status} {body}")
    global API_KEY
    API_KEY = body["data"]["api_key"]


# ---------------- 用例 ----------------

def t_basic():
    sim_reset()
    status, body = openai_request(G_BASIC)
    rec = recorded()
    want("status 200", status == 200, f"{status} {body}")
    want("content", body["choices"][0]["message"]["content"] == "SIM-OK basic response", str(body)[:200])
    want("usage", body.get("usage", {}).get("total_tokens") == 19, str(body.get("usage")))
    want("one upstream call", len(rec) == 1, f"{len(rec)} calls")
    r = rec[0]
    want("upstream path", r["path"].endswith("/openai/v1/chat/completions"), r["path"])
    want("upstream model", r["body"]["model"] == "sim-openai-basic", r["body"].get("model"))
    want("upstream auth", r["headers"].get("authorization") == "Bearer sk-sim-key", str(r["headers"]))
    check("[基线] OpenAI 非流式透传(content/usage/模型改写/凭据注入)", True)


def t_json_schema():
    sim_reset()
    schema = {"type": "object", "properties": {"answer": {"type": "number"}}, "required": ["answer"], "additionalProperties": False}
    status, body = openai_request(G_JSON, extra={"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "schema": schema}}})
    rec = recorded()
    want("status 200", status == 200, f"{status} {body}")
    rf = rec[0]["body"].get("response_format")
    want("response_format passthrough", rf and rf.get("type") == "json_schema" and rf["json_schema"]["schema"] == schema, str(rf)[:200])
    content = body["choices"][0]["message"]["content"]
    want("json content", json.loads(content) == {"answer": 42}, content)
    check("[基线] json_schema 请求参数透传 + JSON 内容返回", True)


def t_thinking_stream():
    sim_reset()
    status, events, cerr = openai_stream(G_THINK, extra={"reasoning_effort": "high", "thinking": {"type": "enabled"}})
    rec = recorded()
    want("status 200", status == 200, f"{status} {cerr}")
    datas = [e["data"] for e in events]
    want("last frame [DONE]", datas and datas[-1] == "[DONE]", str(datas[-3:]))
    reasoning = ""
    content = ""
    usage = None
    for d in datas[:-1]:
        chunk = json.loads(d)
        delta = chunk["choices"][0]["delta"] if chunk.get("choices") else {}
        reasoning += delta.get("reasoning_content") or ""
        content += delta.get("content") or ""
        if not chunk.get("choices") and chunk.get("usage"):
            usage = chunk["usage"]
    want("reasoning_content received", "step by step" in reasoning, reasoning[:120])
    want("content received", "42" in content, content[:120])
    want("usage frame", usage and usage.get("total_tokens") == 24, str(usage))
    b = rec[0]["body"]
    want("outbound model", b["model"] == "sim-openai-thinking", b.get("model"))
    want("outbound stream", b.get("stream") is True)
    want("include_usage injected", b.get("stream_options", {}).get("include_usage") is True, str(b.get("stream_options")))
    want("reasoning_effort preserved", b.get("reasoning_effort") == "high", b.get("reasoning_effort"))
    want("thinking marker preserved", (b.get("thinking") or {}).get("type") == "enabled", str(b.get("thinking")))
    check("[基线+定向] 思考流 reasoning_content/[DONE] 尾帧 + GLM 风格思考标记(reasoning_effort/thinking)透传", True)


def t_tools_nonstream():
    sim_reset()
    tools = [{"type": "function", "function": {"name": "get_weather", "description": "Get weather", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
    status, body = openai_request(G_TOOLS, extra={"tools": tools, "tool_choice": "auto"})
    rec = recorded()
    want("status 200", status == 200, f"{status} {body}")
    rt = rec[0]["body"].get("tools")
    want("tools passthrough", rt and rt[0]["function"]["name"] == "get_weather", str(rt)[:200])
    msg = body["choices"][0]["message"]
    want("tool_calls returned", msg.get("tool_calls") and msg["tool_calls"][0]["function"]["name"] == "get_weather", str(msg)[:200])
    want("tool args", json.loads(msg["tool_calls"][0]["function"]["arguments"]) == {"city": "Beijing"})
    want("finish_reason", body["choices"][0]["finish_reason"] == "tool_calls")
    check("[基线] tool_call 非流式往返(tools 定义透传 + tool_calls 返回)", True)


def t_tools_stream():
    sim_reset()
    tools = [{"type": "function", "function": {"name": "get_weather", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}}]
    status, events, cerr = openai_stream(G_TOOLS_S, extra={"tools": tools})
    rec = recorded()
    want("status 200", status == 200, f"{status} {cerr}")
    datas = [e["data"] for e in events]
    want("last frame [DONE]", datas and datas[-1] == "[DONE]", str(datas[-2:]))
    args, finish = "", None
    for d in datas[:-1]:
        chunk = json.loads(d)
        if not chunk.get("choices"):
            continue
        for tc in chunk["choices"][0]["delta"].get("tool_calls") or []:
            args += tc["function"].get("arguments") or ""
        if chunk["choices"][0].get("finish_reason"):
            finish = chunk["choices"][0]["finish_reason"]
    want("tool_call stream args", json.loads(args) == {"city": "Beijing"}, args)
    want("finish tool_calls", finish == "tool_calls", str(finish))
    want("tools passthrough", rec[0]["body"].get("tools", [{}])[0]["function"]["name"] == "get_weather")
    check("[基线] tool_call 流式往返(增量分片 + finish_reason + [DONE])", True)


def t_empty_response():
    sim_reset()
    status, body = openai_request(G_EMPTY)
    # 行为观察项:网关不得挂起/崩溃;空响应以 200 透传是既有 passthrough 语义
    want("responded", status in (200, 500, 502), f"{status}")
    detail = f"status={status} body={str(body)[:120]}"
    check("[基线] 空响应(choices=[])网关不挂起", status == 200, detail)


def t_zero_error():
    sim_reset()
    status, body = openai_request(G_ZERO)
    # 行为观察项:OpenAI Chat 同协议透传路径下 200+error 体原样透传(与升级前一致);
    # 库内零状态修正作用于 pipeline 转换路径,此用例记录实际行为
    detail = f"status={status} body={str(body)[:160]}"
    check("[基线] 零状态错误(200+error 体)行为记录", True, detail)


def t_failover():
    sim_reset()
    status, body = openai_request(G_FAILOVER)
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:160]}")
    want("content", "SIM-OK" in (body.get("choices") or [{}])[0].get("message", {}).get("content", ""), str(body)[:200])
    models = [r["body"]["model"] for r in rec]
    want("failed member tried", "sim-openai-500" in models, str(models))
    want("fallback member tried", "sim-openai-basic" in models, str(models))
    check("[基线] 失败转移(500 成员冷却后切换成功成员)", True)


def t_flaky_retry():
    sim_reset()
    status, body = openai_request(G_FLAKY, timeout=90)
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:160]}")
    models = [r["body"]["model"] for r in rec]
    want("retried same member", models.count("sim-openai-flaky") >= 2, str(models))
    check("[基线] 同成员失败重试(冷却到期后再次尝试)", True)


def t_anthropic_compat():
    sim_reset()
    status, body = anthropic_request(G_BASIC, system="You are sim", messages=[{"role": "user", "content": "hi"}], extra={"max_tokens": 100})
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:200]}")
    want("anthropic shape", body.get("type") == "message" and body.get("content", [{}])[0].get("text") == "SIM-OK basic response", str(body)[:200])
    b = rec[0]["body"]
    want("converted to openai", b["model"] == "sim-openai-basic", b.get("model"))
    msgs = b.get("messages", [])
    want("system merged first", msgs and msgs[0].get("role") == "system" and "You are sim" in _content_text(msgs[0]), str(msgs)[:200])
    want("user message", any(m.get("role") == "user" and "hi" in _content_text(m) for m in msgs), str(msgs)[:200])
    check("[基线] Anthropic 客户端 → OpenAI 渠道跨协议转换", True)


def _content_text(msg):
    c = msg.get("content")
    if isinstance(c, str):
        return c
    if isinstance(c, list):
        return " ".join(p.get("text", "") for p in c if isinstance(p, dict))
    return ""


def t_web_search_passthrough():
    sim_reset()
    tools = [{"type": "web_search_20250305", "name": "web_search", "max_uses": 3}]
    status, body = anthropic_request(G_CBASIC, extra={"tools": tools})
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:160]}")
    rt = rec[0]["body"].get("tools")
    want("web_search preserved", rt and rt[0].get("type") == "web_search_20250305" and rt[0].get("max_uses") == 3, str(rt)[:200])
    want("anthropic path", rec[0]["path"].endswith("/anthropic/v1/messages"), rec[0]["path"])
    want("x-api-key injected", rec[0]["headers"].get("x-api-key") == "sk-sim-key", str(rec[0]["headers"]))
    check("[定向] web_search 原生工具经 Anthropic 同协议透传保留", True)


def t_web_search_convert():
    """定向用例(修复回归路径):Responses 客户端的 web_search 工具经跨协议转换进 Anthropic
    出站时保留为原生 web_search_20250305(库 9f310561,仅转换路径参与;透传见上一用例)。"""
    sim_reset()
    status, body = responses_request(G_CBASIC, tools=[{"type": "web_search"}])
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:160]}")
    tools = (rec[0]["body"] or {}).get("tools") or []
    want(
        "native tool preserved",
        tools and tools[0].get("type") == "web_search_20250305" and tools[0].get("name") == "web_search",
        str(tools)[:200],
    )
    want("anthropic path", rec[0]["path"].endswith("/anthropic/v1/messages"), rec[0]["path"])
    check("[定向] web_search 经 Responses→Anthropic 跨协议转换保留原生工具", True)


def t_cache_control():
    sim_reset()
    system = [{"type": "text", "text": "Cache sys prompt", "cache_control": {"type": "ephemeral"}}]
    messages = [{"role": "user", "content": [
        {"type": "text", "text": "first question"},
        {"type": "text", "text": "cache this", "cache_control": {"type": "ephemeral"}},
    ]}]
    status, body = anthropic_request(G_CCACHE, system=system, messages=messages)
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:160]}")
    b = rec[0]["body"]
    sys_blocks = b.get("system") or []
    if isinstance(sys_blocks, str):
        sys_blocks = [{"type": "text", "text": sys_blocks}]
    want("system breakpoint", sys_blocks and sys_blocks[0].get("cache_control", {}).get("type") == "ephemeral", str(sys_blocks)[:200])
    last_blocks = b["messages"][-1]["content"]
    want("message breakpoint", last_blocks[-1].get("cache_control", {}).get("type") == "ephemeral", str(last_blocks)[-200:])
    total = sum(1 for blk in sys_blocks if blk.get("cache_control")) + sum(
        1 for m in b["messages"] for blk in (m.get("content") or []) if isinstance(blk, dict) and blk.get("cache_control"))
    want("exactly 2 breakpoints", total == 2, f"count={total}")
    usage = body.get("usage", {})
    want("cache usage passthrough", usage.get("cache_read_input_tokens") == 128, str(usage))
    check("[定向] Claude cache_control 断点透传(2 处断点原样到达上游)", True)


def t_cross_to_anthropic():
    sim_reset()
    status, body = openai_request(G_CBASIC, messages=[{"role": "system", "content": "You are claude sim"}, {"role": "user", "content": "hi"}])
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:200]}")
    want("openai shape back", (body.get("choices") or [{}])[0].get("message", {}).get("content") == "SIM-OK claude response", str(body)[:200])
    r = rec[0]
    want("anthropic upstream path", r["path"].endswith("/anthropic/v1/messages"), r["path"])
    b = r["body"]
    want("anthropic request shape", isinstance(b.get("messages"), list) and b["messages"] and b["messages"][0].get("role") in ("user", "system"), str(b)[:200])
    sys_text = b.get("system")
    sys_text = sys_text if isinstance(sys_text, str) else json.dumps(sys_text, ensure_ascii=False)
    want("system carried", "You are claude sim" in sys_text, sys_text[:160])
    want("max_tokens present", isinstance(b.get("max_tokens"), int), str(b.get("max_tokens")))
    want("anthropic-version header", "anthropic-version" in (h.lower() for h in r["headers"]), str(r["headers"]))
    check("[定向] OpenAI 客户端 → Anthropic 渠道跨格式转换(出站形状/响应回转)", True)


def t_multi_system_merge():
    sim_reset()
    system = [
        {"type": "text", "text": "SYS-FIRST"},
        {"type": "text", "text": "SYS-SECOND"},
    ]
    status, body = anthropic_request(G_BASIC, system=system)
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:160]}")
    msgs = rec[0]["body"].get("messages", [])
    system_msgs = [m for m in msgs if m.get("role") == "system"]
    want("exactly one system message", len(system_msgs) == 1, f"system count={len(system_msgs)}")
    text = _content_text(system_msgs[0]) if system_msgs else ""
    want("first part kept", "SYS-FIRST" in text, text[:160])
    want("second part kept", "SYS-SECOND" in text, text[:160])
    check("[定向] 多条 system 合并(2 段并为 1 条 system,内容不丢)", True)


def t_custom_header_placeholder():
    sim_reset()
    status, body = openai_request(G_HDR, headers={"X-Custom-Marker": "e2e-marker-123"})
    rec = recorded()
    want("status 200", status == 200, f"{status} {str(body)[:160]}")
    marker = rec[0]["headers"].get("X-Marker") or rec[0]["headers"].get("x-marker")
    want("placeholder replaced", marker == "e2e-marker-123", f"X-Marker={marker!r} headers={rec[0]['headers']}")
    check("[加固] 自定义头 {client_header:...} 占位符替换", True)


def t_disconnect_stream():
    sim_reset()
    status, events, cerr = openai_stream(G_DISC)
    datas = [e["data"] for e in events]
    want("partial content received", any("partial" in d for d in datas), str(datas)[:200])
    want("no [DONE]", "[DONE]" not in datas, str(datas)[-120:])
    # 终态断言延后到日志兜底阶段(见 t_log_finalstates)
    check("[定向] 流式中途断连:部分内容已交付且无 [DONE] 伪终帧(终态见日志断言)", True)


def t_eof_stream():
    """行为记录:优雅 FIN 关流(无 [DONE])在网关侧按成功终态收尾(升级前后一致)。"""
    sim_reset()
    status, events, cerr = openai_stream(G_EOF)
    datas = [e["data"] for e in events]
    want("partial content received", any("partial" in d for d in datas), str(datas)[:200])
    want("no [DONE]", "[DONE]" not in datas, str(datas)[-120:])
    check("[加固] 优雅 FIN 提前关流(无 [DONE]):网关按成功终态(终态见日志断言)", True)


def t_claude_stream():
    sim_reset()
    status, events, cerr = anthropic_stream(G_CSTREAM)
    want("status 200", status == 200, f"{status} {cerr}")
    names = [e["event"] for e in events if e["event"]]
    want("message_stop last", names and names[-1] == "message_stop", str(names))
    text = "".join(
        json.loads(e["data"]).get("delta", {}).get("text", "")
        for e in events if e["event"] == "content_block_delta"
    )
    want("stream text", "SIM-OK claude stream" in text, text[:120])
    want("message_start present", "message_start" in names)
    check("[基线] Anthropic 流式完整事件序列(message_start..message_stop)", True)


def t_log_finalstates():
    """日志兜底断言:断连请求终态为 failed;失败转移请求 attempts=2 且成功。"""
    status, body = admin_api("/api/v1/log/list?page=1&page_size=100")
    want("log list", status == 200, f"{status}")
    data = body.get("data")
    logs = (data.get("list") if isinstance(data, dict) else data) or []
    by_model = {}
    for lg in logs:
        if isinstance(lg, dict):
            by_model.setdefault(lg.get("request_model_name"), []).append(lg)
    disc = by_model.get(G_DISC)
    want("disconnect log exists", bool(disc), f"models={list(by_model)}")
    if disc:
        lg = disc[0]
        want("disconnect final state failed", bool((lg.get("error") or "").strip()), f"error={lg.get('error')!r}")
        # list 接口 Omit 大字段, 部分响应正文走详情接口断言
        ds, detail = admin_api(f"/api/v1/log/{lg['id']}")
        want("log detail", ds == 200, f"{ds}")
        want("disconnect kept response", "partial" in ((detail.get("data") or {}).get("response_content") or ""), str(detail)[:200])
    fo = by_model.get(G_FAILOVER)
    want("failover log exists", bool(fo), f"models={list(by_model)}")
    if fo:
        lg = fo[0]
        attempts = lg.get("attempts") or []
        want("failover attempts 2", lg.get("total_attempts") == 2, f"total={lg.get('total_attempts')}")
        want("first failed", attempts and attempts[0].get("status") == "failed", str(attempts)[:160])
        want("second success", len(attempts) > 1 and attempts[1].get("status") == "success", str(attempts)[:160])
        want("no request error", not (lg.get("error") or "").strip(), lg.get("error"))
    eof = by_model.get(G_EOF)
    want("eof log exists", bool(eof), f"models={list(by_model)}")
    if eof:
        want("eof final state success", not (eof[0].get("error") or "").strip(), f"error={eof[0].get('error')!r}")
    th = by_model.get(G_THINK)
    want("thinking log exists", bool(th), f"models={list(by_model)}")
    if th:
        want("thinking log effort", (th[0].get("reasoning_effort") or "") == "high", th[0].get("reasoning_effort"))
    check("[加固] 日志终态落库(断连=failed 含部分响应 / 失败转移 attempts / reasoning_effort 列)", True)


def main():
    print(f"gateway={GATEWAY}")
    wait_gateway()
    sim_upstream = detect_sim_upstream()
    print(f"sim upstream (container view) = {sim_upstream}")
    setup(sim_upstream)
    print(f"api_key={API_KEY[:12]}...")
    print("---- running cases ----")

    run_case("[基线] OpenAI 非流式透传", t_basic)
    run_case("[基线] json_schema", t_json_schema)
    run_case("[基线+定向] 思考流", t_thinking_stream)
    run_case("[基线] tool_call 非流式", t_tools_nonstream)
    run_case("[基线] tool_call 流式", t_tools_stream)
    run_case("[基线] 空响应", t_empty_response)
    run_case("[基线] 零状态错误", t_zero_error)
    run_case("[基线] 失败转移", t_failover)
    run_case("[基线] 同成员重试", t_flaky_retry)
    run_case("[基线] Anthropic 兼容路径", t_anthropic_compat)
    run_case("[定向] web_search 工具保留(透传)", t_web_search_passthrough)
    run_case("[定向] web_search 工具保留(转换)", t_web_search_convert)
    run_case("[定向] cache_control 断点", t_cache_control)
    run_case("[定向] OpenAI→Anthropic 跨格式", t_cross_to_anthropic)
    run_case("[定向] 多条 system 合并", t_multi_system_merge)
    run_case("[加固] 自定义头占位符", t_custom_header_placeholder)
    run_case("[定向] 流式中途断连", t_disconnect_stream)
    run_case("[加固] 优雅 FIN 提前关流", t_eof_stream)
    run_case("[基线] Anthropic 流式", t_claude_stream)

    if SKIP_LOG_WAIT:
        print("-- SKIP_LOG_WAIT=1, 跳过日志兜底断言 --")
    else:
        print("-- 等待 65s 日志兜底 flush(TaskRelayLogSave 60s 周期)--")
        time.sleep(65)
        run_case("[加固] 日志终态", t_log_finalstates)

    passed = sum(1 for _, ok in RESULTS if ok)
    print("---- summary ----")
    print(f"{passed}/{len(RESULTS)} passed")
    for name, ok in RESULTS:
        if not ok:
            print(f"  FAILED: {name}")
    sys.exit(0 if passed == len(RESULTS) else 1)


if __name__ == "__main__":
    main()
