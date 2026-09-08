# ADR-0007: 基线先行、分批移植的执行节奏

- 状态: 已接受(2026-09-08)
- 关联: ADR-0002(范围)、ADR-0008(部署资产)

## 背景

一次全量移植会让集成问题(移植错 vs 上游变更)混杂难定位;先切后补则日常功能(日志持久化、Usage Card)缺位不可用。

## 决策

按以下顺序执行,每步可验证、可回退:

1. **基线**:dev-v2 = 纯上游 v0.13.2 + 部署资产(ADR-0008),空库起容器冒烟
2. **迁移**:转换脚本开发 + `data/` 副本演练(ADR-0005)
3. **功能包**:逐包移植逐包验证,建议顺序:运维刚需+可观测 → 日志持久化 → 识别/主题/思考等级 → Codex/Usage/渠道包
4. **冒烟矩阵**:axonhub/llm 行为等价性验证(下述风险清单),不等价项在新层重修
5. **切换**:停机窗口一次切,发 v2.0.0

## 后果

axonhub/llm 行为冒烟矩阵是最大风险区,至少覆盖:

- DeepSeek thinking 族:非透传/透传回传、数组内容、工具调用 reasoning_content、空 thinking、不合成的 signature(fork 侧 9 个修复的行为语义)
- Anthropic 透传族:reasoning 参数、content_block_start 保留、thinking block 修补、缓存前缀不被透传复写破坏
- json_schema 在 Chat/Responses API 间序列化一致、`strict` 字段往返不丢
- 跨渠道重试时入站适配器状态隔离
- 上游 2xx 空响应/Responses API failed/error 事件触发重试而非静默空响应
- SSE 全生命周期:流式 [DONE] 正常结束不误报断开(上游 `10f0204` 已修,验证即可)
