// compileMemberRegex 以 JS 方言编译成员正则, 供分组成员正则与全局模型过滤等输入预检使用。
// 后端权威引擎 regexp2 认识开头的 (?i) 内联忽略大小写 flag, JS RegExp 不认识,
// 需剥掉并转为原生 i flag, 否则后端合法的写法在前端预检即被误判; 其余语法两引擎同方言。
// 无法编译返回 null, 由调用方走无效/空集分支。
export function compileMemberRegex(pattern: string): RegExp | null {
    const inlineFlag = pattern.startsWith('(?i)');
    try {
        return new RegExp(inlineFlag ? pattern.slice(4) : pattern, inlineFlag ? 'i' : '');
    } catch {
        return null;
    }
}
