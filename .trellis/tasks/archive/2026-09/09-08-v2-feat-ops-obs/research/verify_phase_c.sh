#!/bin/bash
# Step5 Phase C:健康检查 AC1-AC4
# 前置:Phase A/B 已跑完(库里已有 rx-t4/ac-manual 等残留,不影响本阶段)
set -u
BASE=http://127.0.0.1:8081/api/v1
CJ=/tmp/oct-cookies.txt
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS: $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL: $1"; }

# mock 上游:任意 GET 回合法 OpenAI 模型列表 JSON
GW=$(docker network inspect octopus-v2-smoke_default -f '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null)
echo "宿主网关(容器视角): $GW"
python3 -m http.server 18999 --bind 0.0.0.0 >/dev/null 2>&1 &  # 占位,下面用真 handler 替换
MOCKPID=$!
kill $MOCKPID 2>/dev/null
cat > /tmp/mock_upstream.py <<'EOF'
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps({"data":[{"id":"mock-model"}],"models":[{"id":"mock-model"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type","application/json")
        self.send_header("Content-Length",str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self,*a): pass
HTTPServer(("0.0.0.0",18999),H).serve_forever()
EOF
python3 /tmp/mock_upstream.py >/dev/null 2>&1 &
MOCKPID=$!
trap "kill $MOCKPID 2>/dev/null" EXIT
sleep 1
curl -s http://127.0.0.1:18999/v1/models | grep -q mock-model && echo "mock 上游就绪" || { echo "mock 起不来"; exit 1; }

curl -s -c $CJ -X POST $BASE/user/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin"}' >/dev/null
api() { if [ -n "${3:-}" ]; then curl -s -b $CJ -X "$1" "$BASE$2" -H 'Content-Type: application/json' -d "$3"; else curl -s -b $CJ -X "$1" "$BASE$2"; fi }
stat_of() { # channel_id -> "enabled auto_disabled fail_count last_health_at"
  api GET /channel/stats | python3 -c "
import sys,json
d=json.load(sys.stdin)['data']
c=[x for x in d if x['channel_id']==$1]
print(f\"{c[0]['enabled']} {c[0]['auto_disabled']} {c[0]['health_fail_count']} {c[0].get('last_health_error','')[:40]}\" if c else 'MISSING')"
}

echo "=== AC4 前置+加速:间隔=1分钟 阈值=2 ==="
api POST /setting/set '{"key":"health_check_interval","value":"1"}' | grep -q success && ok "间隔设置生效" || bad "间隔设置失败"
api POST /setting/set '{"key":"health_fail_threshold","value":"2"}' | grep -q success && ok "阈值设置生效" || bad "阈值设置失败"

echo "=== AC1 坏渠道自动禁用 ==="
R=$(api POST /channel/create "{\"id\":0,\"name\":\"hc-dead\",\"dialect\":\"generic\",\"enabled\":true,\"base_url\":\"http://127.0.0.1:9\",\"proxy\":false,\"keys\":[{\"name\":\"k1\",\"key\":\"sk-x\",\"enabled\":true}],\"models\":[\"mock-model\"],\"grants\":[{\"model_name\":\"mock-model\",\"key_name\":\"k1\",\"protocols\":8}]}")
HC=$(echo "$R" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['id'])")
[ -n "$HC" ] && ok "坏渠道创建 id=$HC" || bad "创建失败 $R"
echo "  等待探测轮(最长4分钟)…"
for i in $(seq 1 24); do
  sleep 10
  S=$(stat_of $HC)
  set -- $S
  if [ "$2" = "True" ] && [ "$1" = "False" ]; then break; fi
done
S=$(stat_of $HC); set -- $S
[ "$1" = "False" ] && [ "$2" = "True" ] && [ "$3" -ge 2 ] 2>/dev/null && ok "AC1 自动禁用: enabled=$1 auto_disabled=$2 fail=$3 err=$4" || bad "AC1 未禁用: $S"

echo "=== AC2 修复后自动解禁 ==="
api POST /channel/update "{\"id\":$HC,\"name\":\"hc-dead\",\"dialect\":\"generic\",\"enabled\":false,\"base_url\":\"http://$GW:18999\",\"proxy\":false,\"keys\":[{\"name\":\"k1\",\"key\":\"sk-x\",\"enabled\":true}],\"models\":[\"mock-model\"],\"grants\":[{\"model_name\":\"mock-model\",\"key_name\":\"k1\",\"protocols\":8}]}" >/dev/null
echo "  等待下一轮(最长3分钟)…"
for i in $(seq 1 18); do
  sleep 10
  S=$(stat_of $HC); set -- $S
  [ "$1" = "True" ] && [ "$2" = "False" ] && break
done
S=$(stat_of $HC); set -- $S
[ "$1" = "True" ] && [ "$2" = "False" ] && [ "$3" = "0" ] && ok "AC2 自动解禁: enabled=$1 auto_disabled=$2 fail=$3" || bad "AC2 未解禁: $S"

echo "=== AC3 人工禁用不被翻回;人工启用清标记 ==="
api POST /channel/enable "{\"id\":$HC,\"enabled\":false}" >/dev/null
sleep 70  # 跨一个探测轮
S=$(stat_of $HC); set -- $S
[ "$1" = "False" ] && [ "$2" = "False" ] && ok "AC3a 人工禁用渠道探测成功仍保持禁用" || bad "AC3a 被误翻: $S"
api POST /channel/enable "{\"id\":$HC,\"enabled\":true}" >/dev/null
S=$(stat_of $HC); set -- $S
[ "$1" = "True" ] && ok "AC3b 人工启用生效" || bad "AC3b: $S"

echo "=== AC4 间隔=0 关闭任务 ==="
api POST /setting/set '{"key":"health_check_interval","value":"0"}' | grep -q success && ok "间隔设0" || bad "设0失败"
sleep 2
docker logs octopus-v2-smoke 2>&1 | grep -i "health_check_interval.*removed\|task.*removed" | tail -1 | grep -q . && ok "AC4 任务移除日志确认" || { sleep 5; docker logs octopus-v2-smoke 2>&1 | grep -i "removed" | tail -2; bad "AC4 未见移除日志(需人工核对)"; }

echo; echo "==== 结果: PASS=$PASS FAIL=$FAIL ===="
exit $FAIL