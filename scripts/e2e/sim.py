#!/usr/bin/env python3
"""Octopus e2e 冒烟模拟器:多形状假上游(设计见 .trellis/tasks/09-11-bump-axonhub-llm/design.md 层1)。

宿主上直接跑(仅标准库):
    python3 scripts/e2e/sim.py            # 监听 0.0.0.0:18080

容器内网关经 compose 网关回连本机;行为按请求体里的 model 名分发(sim-* 场景名)。
全部收到的请求记录在内存,供驱动脚本断言出站形状:
    GET /__recorded   取记录列表 JSON
    GET /__reset      清空记录与场景计数

场景清单(OpenAI 形 /openai/v1/chat/completions):
    sim-openai-basic          非流式,content+usage
    sim-openai-hdr            同 basic(配合自定义头占位符用例)
    sim-openai-json           非流式,回合法 JSON 文本
    sim-openai-thinking       流式:reasoning_content 分片 + content + usage 块 + [DONE]
    sim-openai-tools          非流式:message.tool_calls
    sim-openai-tools-stream   流式:tool_calls 增量 + finish_reason=tool_calls + [DONE]
    sim-openai-500            恒 500(OpenAI 错误体)
    sim-openai-flaky          首次 500,此后成功(计数随 /__reset 清零)
    sim-openai-zero-error     200 + {"error":{...}}(零状态错误形状)
    sim-openai-empty          200 + choices 为空数组
    sim-openai-disconnect     流式:2 个分片后硬断连 RST(无 [DONE])
    sim-openai-eof            流式:2 个分片后优雅 FIN 关流(无 [DONE];网关按成功终态,行为记录用例)
Anthropic 形 /anthropic/v1/messages:
    sim-claude-basic          非流式 message
    sim-claude-cache          同 basic,usage 带 cache_read_input_tokens
    sim-claude-stream         流式完整事件序列至 message_stop
    sim-claude-disconnect     流式 1 个增量后硬断连
"""

import json
import os
import socket
import struct
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SIM_PORT = int(os.environ.get("SIM_PORT", "18080"))

_lock = threading.Lock()
_recorded = []   # 收到的请求记录
_counters = {}   # 场景计数器(failky 等)

OPENAI_MODELS = [
    "sim-openai-basic",
    "sim-openai-hdr",
    "sim-openai-json",
    "sim-openai-thinking",
    "sim-openai-tools",
    "sim-openai-tools-stream",
    "sim-openai-500",
    "sim-openai-flaky",
    "sim-openai-zero-error",
    "sim-openai-empty",
    "sim-openai-disconnect",
    "sim-openai-eof",
]
ANTHROPIC_MODELS = [
    "sim-claude-basic",
    "sim-claude-cache",
    "sim-claude-stream",
    "sim-claude-disconnect",
]

# 记录时保留的请求头(断言用;丢弃其余避免噪音)
WATCH_HEADERS = [
    "authorization",
    "x-api-key",
    "anthropic-version",
    "content-type",
    "x-marker",
    "x-custom-marker",
    "user-agent",
]


def bump(scenario):
    with _lock:
        _counters[scenario] = _counters.get(scenario, 0) + 1
        return _counters[scenario]


def record(handler, path, body):
    try:
        parsed = json.loads(body) if body else None
    except ValueError:
        parsed = {"__raw__": body.decode("utf-8", "replace") if isinstance(body, bytes) else str(body)}
    entry = {
        "ts": time.time(),
        "method": handler.command,
        "path": path,
        "headers": {k.lower(): v for k, v in handler.headers.items() if k.lower() in WATCH_HEADERS},
        "body": parsed,
    }
    with _lock:
        _recorded.append(entry)


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # 静默访问日志,避免刷屏
        pass

    def finish(self):
        # 断连场景直接关了底层 socket,基类 flush 会抛 OSError,吞掉避免刷 traceback
        try:
            super().finish()
        except OSError:
            pass

    def _sse_headers(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()

    def _hard_reset(self):
        """SO_LINGER=0 后 close 发 RST 而非 FIN:对端读到 ECONNRESET,才是真"中途断连"。

        普通 close(FIN) 在对端解码器里就是 io.EOF,网关会按正常流结束处理(成功终态);
        RST 才能触发失败路径。
        """
        try:
            self.connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
            self.connection.close()
        except OSError:
            pass

    # ---------- 控制端点 ----------
    def do_GET(self):
        if self.path == "/__recorded":
            with _lock:
                payload = json.dumps(_recorded).encode()
            self._json(200, payload)
        elif self.path == "/__reset":
            with _lock:
                _recorded.clear()
                _counters.clear()
            self._json(200, b"[]")
        elif self.path == "/openai/v1/models":
            self._json(200, json.dumps({"object": "list", "data": [{"id": m, "object": "model"} for m in OPENAI_MODELS]}).encode())
        elif self.path == "/anthropic/v1/models":
            self._json(200, json.dumps({"data": [{"id": m, "type": "model", "display_name": m} for m in ANTHROPIC_MODELS]}).encode())
        else:
            self._json(404, b'{"error":{"message":"not found"}}')

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""
        record(self, self.path, body)
        try:
            req = json.loads(body) if body else {}
        except ValueError:
            req = {}
        model = req.get("model") or ""
        streaming = bool(req.get("stream"))

        if self.path.endswith("/openai/v1/chat/completions"):
            self.handle_openai(model, streaming)
        elif self.path.endswith("/anthropic/v1/messages"):
            self.handle_anthropic(model, streaming)
        else:
            self._json(404, b'{"error":{"message":"not found"}}')

    # ---------- OpenAI 形 ----------
    def handle_openai(self, model, streaming):
        if model == "sim-openai-500":
            self._json(500, json.dumps({"error": {"message": "sim upstream failure", "type": "server_error", "code": 500}}).encode())
            return
        if model == "sim-openai-flaky":
            if bump(model) == 1:
                self._json(500, json.dumps({"error": {"message": "sim flaky first failure", "type": "server_error"}}).encode())
                return
            self._completion(model, "SIM-OK flaky recovery")
            return
        if model == "sim-openai-zero-error":
            self._json(200, json.dumps({"error": {"message": "sim zero-state error", "type": "server_error"}}).encode())
            return
        if model == "sim-openai-empty":
            self._json(200, json.dumps({
                "id": "chatcmpl-sim-empty", "object": "chat.completion", "created": 1700000000, "model": model,
                "choices": [], "usage": {"prompt_tokens": 5, "completion_tokens": 0, "total_tokens": 5},
            }).encode())
            return
        if model == "sim-openai-thinking" and streaming:
            self.thinking_stream(model)
            return
        if model == "sim-openai-tools" and not streaming:
            self._json(200, json.dumps({
                "id": "chatcmpl-sim-tools", "object": "chat.completion", "created": 1700000000, "model": model,
                "choices": [{
                    "index": 0,
                    "message": {
                        "role": "assistant", "content": None,
                        "tool_calls": [{
                            "id": "call_sim_1", "type": "function",
                            "function": {"name": "get_weather", "arguments": "{\"city\":\"Beijing\"}"},
                        }],
                    },
                    "finish_reason": "tool_calls",
                }],
                "usage": {"prompt_tokens": 30, "completion_tokens": 12, "total_tokens": 42},
            }).encode())
            return
        if model == "sim-openai-tools-stream" and streaming:
            self.tool_stream(model)
            return
        if model == "sim-openai-disconnect" and streaming:
            self.disconnect_stream(model)
            return
        if model == "sim-openai-eof" and streaming:
            self.eof_stream(model)
            return
        # basic / hdr / json / 非流式默认
        content = '{"answer": 42}' if model == "sim-openai-json" else "SIM-OK basic response"
        self._completion(model, content)

    def _completion(self, model, content):
        self._json(200, json.dumps({
            "id": "chatcmpl-sim-basic", "object": "chat.completion", "created": 1700000000, "model": model,
            "choices": [{"index": 0, "message": {"role": "assistant", "content": content}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 12, "completion_tokens": 7, "total_tokens": 19},
        }).encode())

    def thinking_stream(self, model):
        chunks = [
            {"choices": [{"index": 0, "delta": {"role": "assistant", "reasoning_content": "Let me think step by step."}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {"reasoning_content": " The answer is 42."}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {"content": "The answer is"}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {"content": " 42."}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]},
            {"choices": [], "usage": {"prompt_tokens": 15, "completion_tokens": 9, "total_tokens": 24}},
        ]
        self._sse_openai(model, chunks)

    def tool_stream(self, model):
        chunks = [
            {"choices": [{"index": 0, "delta": {"role": "assistant", "tool_calls": [{"index": 0, "id": "call_sim_1", "type": "function", "function": {"name": "get_weather", "arguments": ""}}]}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, "function": {"arguments": "{\"ci"}}]}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, "function": {"arguments": "ty\":\"Beijing\"}"}}]}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}]},
        ]
        self._sse_openai(model, chunks)

    def disconnect_stream(self, model):
        # 发 2 个分片后硬断连:不给 [DONE],直接关底层 socket,模拟上游中途掉线
        chunks = [
            {"choices": [{"index": 0, "delta": {"role": "assistant", "content": "partial"}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {"content": " before disconnect"}, "finish_reason": None}]},
        ]
        self._sse_headers()
        for chunk in chunks:
            payload = {"id": "chatcmpl-sim-disc", "object": "chat.completion.chunk", "created": 1700000000, "model": model}
            payload.update(chunk)
            self.wfile.write(b"data: " + json.dumps(payload).encode() + b"\n\n")
            self.wfile.flush()
        self._hard_reset()

    def eof_stream(self, model):
        """优雅 FIN 关流(无 [DONE]):网关解码器视为 io.EOF,按成功终态收尾(与升级前一致)。"""
        chunks = [
            {"choices": [{"index": 0, "delta": {"role": "assistant", "content": "partial"}, "finish_reason": None}]},
            {"choices": [{"index": 0, "delta": {"content": " then eof"}, "finish_reason": None}]},
        ]
        self._sse_headers()
        for chunk in chunks:
            payload = {"id": "chatcmpl-sim-eof", "object": "chat.completion.chunk", "created": 1700000000, "model": model}
            payload.update(chunk)
            self.wfile.write(b"data: " + json.dumps(payload).encode() + b"\n\n")
            self.wfile.flush()
        self.close_connection = True

    def _sse_openai(self, model, chunks):
        self._sse_headers()
        for chunk in chunks:
            payload = {"id": "chatcmpl-sim", "object": "chat.completion.chunk", "created": 1700000000, "model": model}
            payload.update(chunk)
            self.wfile.write(b"data: " + json.dumps(payload).encode() + b"\n\n")
            self.wfile.flush()
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()
        self.close_connection = True

    # ---------- Anthropic 形 ----------
    def handle_anthropic(self, model, streaming):
        if model == "sim-claude-stream" and streaming:
            self.claude_stream(model)
            return
        if model == "sim-claude-disconnect" and streaming:
            self.claude_disconnect(model)
            return
        usage = {"input_tokens": 10, "output_tokens": 5}
        if model == "sim-claude-cache":
            usage["cache_read_input_tokens"] = 128
            usage["cache_creation_input_tokens"] = 64
        self._json(200, json.dumps({
            "id": "msg_sim_1", "type": "message", "role": "assistant", "model": model,
            "content": [{"type": "text", "text": "SIM-OK claude response"}],
            "stop_reason": "end_turn", "stop_sequence": None, "usage": usage,
        }).encode())

    def claude_stream(self, model):
        events = [
            ("message_start", {"type": "message_start", "message": {"id": "msg_sim_s", "type": "message", "role": "assistant", "model": model, "content": [], "usage": {"input_tokens": 10, "output_tokens": 1}}}),
            ("content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}),
            ("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "SIM-OK "}}),
            ("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "claude stream"}}),
            ("content_block_stop", {"type": "content_block_stop", "index": 0}),
            ("message_delta", {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 6}}),
            ("message_stop", {"type": "message_stop"}),
        ]
        self._sse_headers()
        for name, data in events:
            self.wfile.write(f"event: {name}\ndata: {json.dumps(data)}\n\n".encode())
            self.wfile.flush()
        self.close_connection = True

    def claude_disconnect(self, model):
        self._sse_headers()
        self.wfile.write(f"event: message_start\ndata: {json.dumps({'type': 'message_start', 'message': {'id': 'msg_sim_d', 'type': 'message', 'role': 'assistant', 'model': model, 'content': [], 'usage': {'input_tokens': 10, 'output_tokens': 1}}})}\n\n".encode())
        self.wfile.flush()
        self.wfile.write(b"event: content_block_delta\ndata: " + json.dumps({"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "partial"}}).encode() + b"\n\n")
        self.wfile.flush()
        self._hard_reset()

    # ---------- 基础工具 ----------
    def _json(self, status, payload):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


def main():
    server = ThreadingHTTPServer(("0.0.0.0", SIM_PORT), Handler)
    print(f"sim listening on 0.0.0.0:{SIM_PORT}")
    server.serve_forever()


if __name__ == "__main__":
    main()
