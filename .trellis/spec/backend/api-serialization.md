# API 序列化空值契约(Go nil 切片 ↔ 前端)

## 契约:后端 nil 切片序列化为 JSON `null`,前端消费端点必须归一化

**问题**:Go 的 nil 切片 `[]T{}`(未初始化)经 `json.Marshal` 输出 `null` 而非 `[]`。gorm 里列为空(NULL/空串)读出即 nil 切片,`serializer:json` 列同理。前端若把 API 数组直接塞进 state 再 `.map`/`.filter`,在存量数据为空的渠道上必崩。

**实测案例(2026-09-10)**:迁移库 101/118 渠道 `custom_header` 为空 → `/channel/detail` 返回 `null` → 表单 `fromChannel` 原样透传 → ①管理端测试路径 `toChannelConfig` 里 `.filter` 抛 `TypeError: Cannot read properties of null`;②渠道编辑「高级」步骤 `.map` 渲染崩(整个步骤空白)。

**约定**:

- **前端**:表单/组件入口(fromXxx 这类"API→state"边界)对数组字段一律归一化 `field ?? []`,归一化只做一次,消费端不再各自防御
- **后端**:响应组装处不做特殊处理(改 Marshal 行为影响面大);但**新增 API 设计时**,数组字段尽量给非 nil 初值(`make([]T, 0)`),从源头减少 `null`
- **新增消费 API 数组的表单时**:检查该列在存量/迁移库中的空值占比,空值非零就必须归一化

## Wrong vs Correct

```typescript
// Wrong: 原样透传,存量空值渠道上 .map/.filter 崩
custom_header: channel.custom_header,

// Correct: API→state 边界归一化一次
custom_header: channel.custom_header ?? [],
```

## 测试要求

- 后端单测构造 nil 切片字段的响应,前端(tsc 层面无测试)靠冒烟:起容器后对**迁移库空值渠道**逐项过 UI(octopus-verify 人审清单里显式包含一条空值渠道)
- 回归锚点:`web/src/components/modules/channel/state.ts` `fromChannel` 的 `custom_header ?? []`
