# 执行计划

前置:D1/D2 两个开放决策定案(design.md),否则脚本按默认值实现并在报告标注。

## 步骤

1. [x] 写 `scripts/migrate_v1_to_v2.py`(映射按 design.md 逐表实现,含对账报告与退出码)
   - 验证:`python3 scripts/migrate_v1_to_v2.py --help`;对空/坏源库给出清晰报错
2. [x] 副本演练:活库快照用 SQLite backup API(ro 连接,2026-09-08 实测 cp 活库不安全)→ 跑脚本 → 对账全绿(已过,快照漂移说明见 research)
   - 验证:行数对账表与 prd 验收数字一致;抽查明细(1 渠道全字段、1 组 relay_config JSON、孤儿模型补入构造用例)
3. [x] sqlite 级检查:PRAGMA foreign_key_check 0 违规;sqlite_sequence 各表 seq≥max(id)(2026-09-08 过)
4. [x] 容器冒烟:migtest 容器挂 dst 库(8082/18082)
   - 启动无迁移重跑/无报错;渠道 117、分组 11(10+别名)经 API 与库双确认
   - 选路测试:组 9(deepseek-v4-flash-test)真实上游 3.4s 返回 200,统计落盘(渠道/模型/日统计)
   - **过程中发现并修复 base_url 双叠 bug**(见 research「v1 后缀归一化」):fork URL 带 /v1 后缀 + v2 路径自带 /v1 前缀 → /v1/v1/... 404 → 全成员失败 → 应用按设计无限等待 → 客户端挂死;echo 实验实证后脚本加归一化,修复后复测通过
5. [x] 幂等重跑:两轮 md5 一致;backup API 新快照重跑全绿(2026-09-08,含归一化后版本)
6. [ ] 质量检查(trellis-check):脚本代码质量与 spec 符合性(进行中)
7. [x] 用户过目:18082 页面人工检查通过(2026-09-08 用户确认"没什么问题");D2 终裁=去掉别名组(脚本已改,复测通过)
8. [ ] 更新父任务 prd.md 任务地图勾选;research 笔记补充演练实测数字;更新 spec(若形成可复用约定)

## 回滚点

- 脚本为新文件,演练产物在 /tmp 与 data-v2 之外,不触碰生产与仓库现有数据 → git checkout 即回滚
- 严禁:写 data/data.db、覆盖 data-v2/data.db(模板)、动生产目录

## 评审门

- 步骤 2 后:对账报告给用户过目再进容器冒烟
- 步骤 4 后:页面截图/请求结果给用户确认
