# ②⑤ 原生覆盖验证 + AC 实测记录(2026-09-09)

环境:smoke 容器 octopus-v2-smoke(8081),二进制 v0.13.2-8-g34cf007+本任务改动,空模板库(data-v2/data.db,测前快照 `data-v2-template-snapshot.db`,测后已还原)。

## ② GroupItem 孤儿对账 —— 上游原生覆盖,确认

- 手动组建 2 成员(t1.acm-a + t2.acm-b)→ DELETE /channel/delete/1 → 组内 t1 成员随外键 `OnDelete:CASCADE` 即时消失,t2 成员保留(available=false 因渠道已禁用)。
- 结论:fork `6f8e156` 对账任务在 v2 schema 下无存在必要。脚本:`verify_phase_ab.sh`,结果:`verify_phase_ab.result.txt`(PASS 1/5 行)。

## ⑤ 分组渠道名 DTO —— 上游原生覆盖,确认

- 建组响应 items 直带 `channel_name/channel_id/model_name/key_name/protocols/available`;禁用渠道后其成员 `available=false`。
- 结论:fork `8185f12`(含"禁用/已删除态区分")已被上游等价实现。同上脚本 PASS 2-4 行。

## AC5/AC6 分组正则(全部通过,`verify_phase_ab.result.txt` Phase B)

- 建正则组 `^rxm-1$` 响应即吸纳 2 渠道授权(rx-t3、rx-t4)
- 渠道加凭据(k2)后即时重算:命中模型全部凭据纳入(成员 2→3)
- 渠道模型改失配(rxm-1→仅 rxm-2):成员 3→1
- 手动组全程零扰动(AC6)
- 删渠道:正则组自动清空

## AC1-AC4 健康检查(全部通过,`verify_phase_c.result.txt`)

- AC1 坏渠道(http://127.0.0.1:9)阈值2、间隔1分钟:2 轮后 enabled=false + auto_disabled=true,`last_health_error` 可见
- AC2 base_url 改指向本机 mock 上游(172.20.0.1:18999):下一轮自动解禁 enabled=true + auto_disabled=false + fail=0
- AC3a 人工禁用的健康渠道跨轮探测仍保持禁用;AC3b 人工启用生效
- AC4 间隔设 0:容器日志确认任务移除("removed: interval is 0")

## AC7(正则组 UI 只读)与徽标人审

- 留待 octopus-verify 闭环的人审 UI 环节一并确认(8081)。

## 迁移演练影响说明(父任务要求)

**零影响**。channels 加 4 列、groups 加 member_regex 均为 gorm AutoMigrate 增量加列(零值默认),转换脚本产出库启动即自动补列;member_regex 空=手动组,存量 2236 组员行为不变;健康字段不预填,首轮探测自然填充;本任务未触碰 relay_logs/日志链路。
