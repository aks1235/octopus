# 分组成员正则(member_regex)方言契约

> `groups.member_regex` 的书写语法、前后端引擎差异与预检口径。2026-09-11 由 v2.0.2 修复沉淀(此前前端预检用 JS 原生 RegExp,把后端合法的 `(?i)` 写法误报「正则表达式无效」)。

---

## 场景:member_regex 跨层契约(前端预检/预览 ↔ 后端权威编译)

### 1. 范围 / 触发

member_regex 由用户在分组编辑器输入,前端做可编译性预检与「按正则吸纳」预览,后端保存时用 regexp2 重算成员。**两个引擎方言不一致**,任何一侧单独校验都会出现"另一侧合法/非法"的错位。

### 2. 签名与实现位置

- 后端权威编译:`internal/op/group.go` `syncRegexGroupItems` → `regexp2.Compile(memberRegex, regexp2.ECMAScript)`(引擎 `github.com/dlclark/regexp2`)
- 前端预检/预览:`web/src/components/modules/group/Editor.tsx` `compileMemberRegex(pattern): RegExp | null`(唯一入口,见下方 Wrong/Correct)

### 3. 契约(用户书写约定)

| 项 | 约定 | 违反后果 |
|---|---|---|
| 语法形式 | **纯 pattern**,如 `^...$`;禁止 JS 字面量 `/.../` 定界符 | 斜杠与 flag 后缀被当**字面字符**,编译**不报错**但恒不匹配 → 0 成员,静默失效 |
| 忽略大小写 | pattern **开头**写内联 flag `(?i)` | 无其他写法;JS 的 `/i` 后缀形式非法(见上) |
| 前瞻/后顾 | 支持(lookahead/lookbehind),regexp2 ECMAScript 模式实现 | — |
| 匹配对象与口径 | 全部渠道全部模型名,部分匹配即命中;不看渠道/凭据启停 | — |

### 4. 验证与错误矩阵

| 条件 | 行为 |
|---|---|
| 后端 `regexp2.Compile` 失败 | 保存返回 400(`failed to compile member regex`) |
| 前端预检不可编译 | 表单提示「正则表达式无效」并阻止提交 |
| 编译成功但语义错(如残留 `/` 字面量) | **无任何报错,成员为空**——排障先查正则形式,再查数据 |

### 5. 正反例

- Good:`(?i)^(?!.*(?:flash|search)).*glm-5\.3.*$` —— 忽略大小写 + 负向前瞻排除
- Base:`^glm-5\.3$` —— 精确名
- Bad:`/^(?!.*flash).*glm-5\.3.*$/i` —— JS 字面量语法,恒 0 匹配且不报错

### 6. 测试要求

- 改动 Editor 预检/预览逻辑时:验证 `(?i)` 前缀剥离后 `i` flag 生效(大小写混合模型名均命中)、无前缀时行为不变、非法 pattern 返回 null 走无效分支
- 改动后端编译口径时:Go 侧用 regexp2 同参数写对应用例,保持两侧匹配集合一致

### 7. Wrong vs Correct(前端实现)

#### Wrong

```ts
new RegExp(trimmedMemberRegex); // JS RegExp 不认识 (?i),合法写法被误判无效
```

#### Correct

```ts
// Editor.tsx compileMemberRegex:剥离开头 (?i) 转为原生 i flag,其余语法两引擎同方言
function compileMemberRegex(pattern: string): RegExp | null {
    const inlineFlag = pattern.startsWith('(?i)');
    try {
        return new RegExp(inlineFlag ? pattern.slice(4) : pattern, inlineFlag ? 'i' : '');
    } catch {
        return null;
    }
}
```

**新增任何消费 member_regex 的前端逻辑,一律经 `compileMemberRegex`,不得裸用 `new RegExp`。**

---

## 相关

- 正则吸纳不看启停、`Available` 展示口径 → [relay-routing.md](./relay-routing.md)「禁用 ≠ 删除」
