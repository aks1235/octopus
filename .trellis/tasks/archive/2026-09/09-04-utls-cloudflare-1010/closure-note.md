# 任务关闭说明

## 根因重新定位

原 PRD 认为根因是 **Go TLS 的 JA3 指纹被 Cloudflare 识别**，计划引入 uTLS 伪装浏览器 ClientHello。

实际调查发现：**真正原因是客户端 User-Agent 是 `Python-urllib/3.x`**。

## 验证结果

修改 ai-search 脚本，在两处 HTTP 请求中加入浏览器 User-Agent：
- `chat_request()` (调用 LLM)  
- `http_post_json()` (调用 Tavily/Exa)

修改后测试通过，不再触发 1010 错误。

## 决策

1. ❌ **不需要 uTLS**  
   - Cloudflare 只检查了 User-Agent，没有校验 TLS 指纹
   - 引入 uTLS 会带来大量改动（client、helper、model、前端），收益不明确
   
2. ✅ **已修复 ai-search**  
   - 文件：`~/.claude/skills/ai-search/ai_search.py`
   - 改动：两处 `headers` 加 `User-Agent: Mozilla/5.0 ...`
   
3. ⚠️ **后续预防**  
   - 如果将来遇到**同时检查 TLS 指纹**的 Cloudflare 规则，可重新评估 uTLS
   - 目前没有渠道报告此类问题

## 已回滚改动

- `internal/helper/channel.go` (uTLS log)
- `docker-compose.yml` (测试镜像标签)
- 工作区干净，无遗留文件

## 相关文件

- ai-search 脚本：`~/.claude/skills/ai-search/ai_search.py`
- 任务规划产物：`prd.md`, `design.md`, `implement.md` (保留作为技术参考)
