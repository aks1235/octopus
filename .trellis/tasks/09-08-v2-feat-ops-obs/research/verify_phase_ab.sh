#!/bin/bash
# Step4/5 Phase A/B:②⑤ 原生覆盖验证 + 分组正则 AC5/AC6
# 前置:smoke 容器 8081 已起新二进制;数据为空模板
set -u
BASE=http://127.0.0.1:8081/api/v1
CJ=/tmp/oct-cookies.txt
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS: $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL: $1"; }
jq_get() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(\"d$1\"))" 2>/dev/null; }

curl -s -c $CJ -X POST $BASE/user/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin"}' >/dev/null
api() { # method path data
  if [ -n "${3:-}" ]; then curl -s -b $CJ -X "$1" "$BASE$2" -H 'Content-Type: application/json' -d "$3";
  else curl -s -b $CJ -X "$1" "$BASE$2"; fi
}
mkchan() { # name model extra_models_json -> 创建单key渠道
  api POST /channel/create "{\"id\":0,\"name\":\"$1\",\"dialect\":\"generic\",\"enabled\":true,\"base_url\":\"http://127.0.0.1:9\",\"proxy\":false,\"keys\":[{\"name\":\"k1\",\"key\":\"sk-test\",\"enabled\":true}],\"models\":$2,\"grants\":[{\"model_name\":\"$(echo $2 | python3 -c 'import sys,json;print(json.load(sys.stdin)[0])')\",\"key_name\":\"k1\",\"protocols\":8}]}"
}

echo "=== Phase A: ②⑤ 原生覆盖 ==="
R=$(mkchan ac-t1 '["acm-a"]'); T1=$(echo "$R" | jq_get "['data']['id']")
R=$(mkchan ac-t2 '["acm-b"]'); T2=$(echo "$R" | jq_get "['data']['id']")
[ -n "$T1" ] && [ -n "$T2" ] && ok "测试渠道创建 t1=$T1 t2=$T2" || bad "渠道创建失败: $R"

# 拿两渠道 grant id
R=$(api GET /channel/grants)
G1=$(echo "$R" | python3 -c "import sys,json;[print(g['id']) for g in json.load(sys.stdin)['data'] if g['channel_id']==$T1]")
G2=$(echo "$R" | python3 -c "import sys,json;[print(g['id']) for g in json.load(sys.stdin)['data'] if g['channel_id']==$T2]")

# 手动组:两个成员(⑤:响应应带 channel_name 渠道名 DTO)
R=$(api POST /group/create "{\"name\":\"ac-manual\",\"mode\":\"manual\",\"items\":[{\"channel_grant_id\":$G1},{\"channel_grant_id\":$G2}]}")
NAMES=$(echo "$R" | jq_get "['data']['items'][0]['channel_name']")
[ "$NAMES" = "ac-t1" ] && ok "⑤ 分组成员 DTO 带 channel_name=$NAMES" || bad "⑤ channel_name 缺失: $R"
AV=$(echo "$R" | jq_get "['data']['items'][1]['available']")
[ "$AV" = "True" ] && ok "⑤ 成员 available 初始 True" || bad "⑤ available 异常: $R"

# 禁用 t2 → 其成员 available=false(⑤禁用态)
api POST /channel/enable "{\"id\":$T2,\"enabled\":false}" >/dev/null
R=$(api GET /group/list)
AV2=$(echo "$R" | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print([g['available'] for gr in d if gr['name']=='ac-manual' for g in gr['items'] if g['channel_name']=='ac-t2'][0])")
[ "$AV2" = "False" ] && ok "⑤ 禁用渠道后成员 available=False" || bad "⑤ 禁用态未表达: $R"

# 删 t1 → 其组员级联消失(②)
CNT_BEFORE=$(api GET /group/list | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print(len([gr for gr in d if gr['name']=='ac-manual'][0]['items']))")
api DELETE /channel/delete/$T1 >/dev/null
R=$(api GET /group/list)
ITEMS=$(echo "$R" | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print([ (i['channel_name'],i['available']) for gr in d if gr['name']=='ac-manual' for i in gr['items']])")
echo "$ITEMS" | grep -q "ac-t2" && ! echo "$ITEMS" | grep -q "ac-t1" && ok "② 删渠道后组员级联清理,余:$ITEMS (前:$CNT_BEFORE)" || bad "② 级联异常: $ITEMS"

echo "=== Phase B: AC5/AC6 分组正则 ==="
R=$(mkchan rx-t3 '["rxm-1"]'); T3=$(echo "$R" | jq_get "['data']['id']")
R=$(mkchan rx-t4 '["rxm-1","rxm-2"]'); T4=$(echo "$R" | jq_get "['data']['id']")
R=$(api POST /group/create "{\"name\":\"ac-regex\",\"mode\":\"failover\",\"member_regex\":\"^rxm-1$\",\"items\":[]}")
N=$(echo "$R" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['data']['items']))")
NAMES=$(echo "$R" | python3 -c "import sys,json;print(sorted(i['channel_name'] for i in json.load(sys.stdin)['data']['items']))")
[ "$N" = "2" ] && ok "AC5 建正则组即吸纳 rxm-1 全渠道授权: $NAMES" || bad "AC5 吸纳数=$N: $R"

# 渠道加模型→即时入组;多key→命中模型凭据全纳入
api POST /channel/update "{\"id\":$T4,\"name\":\"rx-t4\",\"dialect\":\"generic\",\"enabled\":true,\"base_url\":\"http://127.0.0.1:9\",\"proxy\":false,\"keys\":[{\"name\":\"k1\",\"key\":\"sk-a\",\"enabled\":true},{\"name\":\"k2\",\"key\":\"sk-b\",\"enabled\":true}],\"models\":[\"rxm-1\",\"rxm-2\"],\"grants\":[{\"model_name\":\"rxm-1\",\"key_name\":\"k1\",\"protocols\":8},{\"model_name\":\"rxm-1\",\"key_name\":\"k2\",\"protocols\":8},{\"model_name\":\"rxm-2\",\"key_name\":\"k1\",\"protocols\":8}]}" >/dev/null
N=$(api GET /group/list | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print(len([gr for gr in d if gr['name']=='ac-regex'][0]['items']))")
[ "$N" = "3" ] && ok "AC5 多凭据全纳入+模型增补即时入组 (成员=$N)" || bad "AC5 增补后成员=$N"

# 渠道改模型去掉 rxm-1 → 失配移除
api POST /channel/update "{\"id\":$T4,\"name\":\"rx-t4\",\"dialect\":\"generic\",\"enabled\":true,\"base_url\":\"http://127.0.0.1:9\",\"proxy\":false,\"keys\":[{\"name\":\"k1\",\"key\":\"sk-a\",\"enabled\":true}],\"models\":[\"rxm-2\"],\"grants\":[{\"model_name\":\"rxm-2\",\"key_name\":\"k1\",\"protocols\":8}]}" >/dev/null
N=$(api GET /group/list | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print(len([gr for gr in d if gr['name']=='ac-regex'][0]['items']))")
NAMES=$(api GET /group/list | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print([i['channel_name'] for gr in d if gr['name']=='ac-regex' for i in gr['items']])")
[ "$N" = "1" ] && ok "AC5 渠道失配模型自动移除 (余=$NAMES)" || bad "AC5 移除异常: $N $NAMES"

# 手动组零影响(AC6):仍只有 ac-t2 一名成员
MN=$(api GET /group/list | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print(len([gr for gr in d if gr['name']=='ac-manual'][0]['items']))")
[ "$MN" = "1" ] && ok "AC6 手动组零影响 (成员=$MN)" || bad "AC6 手动组被扰动: $MN"

# 删渠道 → 组员随级联+正则组重算移除
api DELETE /channel/delete/$T3 >/dev/null
N=$(api GET /group/list | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print(len([gr for gr in d if gr['name']=='ac-regex'][0]['items']))")
[ "$N" = "0" ] && ok "AC5 删渠道后正则组自动清空" || bad "AC5 删渠道后成员=$N"

echo; echo "==== 结果: PASS=$PASS FAIL=$FAIL ===="
exit $FAIL