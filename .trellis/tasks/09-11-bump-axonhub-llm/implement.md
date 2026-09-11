# 执行计划:升级 axonhub/llm

前置:trellis-before-dev 载入 backend spec(relay-routing/relay-log-persistence)。

## 步骤

1. [x] 容器内升级依赖(go get llm@3786f2c5c8de + x/crypto + x/net,tidy)+ 对照 `git show 1bd2ed8:go.mod` 核版本
   - 验证:编译 OK;go.mod/go.sum diff 仅含预期条目
2. [x] 全量单测(容器,-count=1)
3. [x] 模拟器资产落 `scripts/e2e/`(多形状假上游 + 驱动断言脚本)
   - 验证:层 1 全项 PASS/FAIL 输出
4. [x] 基线回归(原 A2 项)+ 定向用例跑通;FAIL 项按 design 失败处理定性
5. [x] 8081 起容器(compose verify 姿势)+ 真渠道抽查(GLM-5.3 必测;DeepSeek/Claude 各一)
   - 验证:用户点验或真渠道响应断言
6. [x] octopus-verify 全流程(重建二进制镜像、健康检查、UI 人审)
7. [x] spec/journal:行为差异清单沉淀(如有);llm 版本记录
8. [ ] commit;用户确认后 v2.0.4 发布(octopus-publish:tag → CI → 生产)

## 回滚点

- 任一步失败可 `git checkout HEAD -- go.mod go.sum` 回滚依赖,零残留。

## 风险提示

- 预期零代码适配;若编译错按最小改动适配并记录。
- 真渠道抽查依赖用户配合(渠道凭据/发请求),提前约。
