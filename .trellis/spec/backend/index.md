# Backend Spec Index

| 文件 | 内容 |
|---|---|
| [relay-log-persistence.md](./relay-log-persistence.md) | 转发日志持久化契约:表形状冻结、009 拦截与回滚规程、终态挂钩模式、迁移条件携带、queryKey 纪律(任务5/6/7 依赖) |
| [relay-routing.md](./relay-routing.md) | 选路与渠道禁用契约:ChannelGrantGet 唯一裁决点、禁用成员选路层过滤(不选/不探测/不计数)、亲和立即失效、gorm default:true 零值坑、buildOutbound 合成临时 grant 复用模式(测试/探测类端点) |
| [group-member-regex.md](./group-member-regex.md) | 分组成员正则方言契约:纯 pattern 禁 JS 斜杠定界符、忽略大小写用开头 (?i)、regexp2↔JS 引擎差异、前端必经 compileMemberRegex、斜杠残留静默 0 成员排障 |
| [api-serialization.md](./api-serialization.md) | API 序列化空值契约:Go nil 切片→JSON null,前端 API→state 边界必须 `?? []` 归一化(迁移库空值触发崩溃的防护) |
| [database-guidelines.md](./database-guidelines.md) | 数据库层规范 |
| [directory-structure.md](./directory-structure.md) | 目录结构规范 |
| [error-handling.md](./error-handling.md) | 错误处理规范 |
