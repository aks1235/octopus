# 实施计划:首页统计日期化

> 前置:prd.md / design.md。**在 v2.1.1 发版后开工**(工作区需保持干净待发版)。

## 执行清单

### 步骤 1:Schema 与迁移

- [ ] `internal/model/stats.go`:StatsHourly 改复合主键 (Date, Hour),注释更新。
- [ ] `internal/db/migrate/013.go`:RegisterBeforeAutoMigration,真实 Up——老表有数据时按行内 date 并入重建(HasTable 防御,幂等);注释写明 nil Up 会炸 InitDB 的教训。

### 步骤 2:op 层

- [ ] `internal/op/stats.go`:statsHourlyCache 改按 (date,hour) 组织;StatsHourlyUpdate 写 (today, nowHour);StatsSaveDB 落盘 upsert;StatsHourlyGet(date) 替换无参版。
- [ ] `internal/op/log.go`:RelayLogSaveDBTask 清理段顺手删超保留期的小时行。

### 步骤 3:API

- [ ] `internal/server/handlers/stats.go`(或所在文件):hourly 接口加 date 参数(默认今天);新增 `GET /stats/rank?date=` 按天聚合(relay_logs GROUP BY,超保留期/关闭返回空)。
- [ ] handler 单测:聚合正确性、保留期边界。

### 步骤 4:前端

- [ ] store 加 selectedDate;activity 格子点击+选中高亮;index 页头日期+回到今天;chart 按 date 查询+空态+历史日停刷新;rank 按天/累计 Tab;api/stats.ts 两端点改造;i18n 三语。

### 步骤 5:验证

- [ ] 容器内 go vet/test 全绿(含新 op 测试:跨天两行/迁移幂等/Get(date));tsc+eslint+build。
- [ ] octopus-verify 全流程 + UI 人审(AC1-AC6)。

## 验证命令

```bash
docker run --rm -v "$PWD":/src -w /src -v octopus-go-mod:/go/pkg/mod -v octopus-go-build-cache:/root/.cache/go-build \
  golang:1.26 sh -c "CGO_ENABLED=0 go test -tags=jsoniter -buildvcs=false ./internal/... ./cmd/..."
cd web && npx tsc --noEmit && npx eslint .
```

## 回滚点

- 迁移幂等可重跑;revert 单 commit 回滚全部。

## 审查门

- 步骤 2 完成后:自查跨天边界(午夜翻日时 Update 写新行不覆盖旧行)。
- 提交前:既有 stats/log 相关测试零语义改动全绿。
