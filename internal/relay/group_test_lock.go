package relay

import "sync"

// groupTests 是"每个分组同一时刻只允许一轮成员测试"的闸。
// 手动测试(非流式 / 流式)与定时探测共用同一把闸: 否则用户点"测试全部成员"的同时定时任务也在打同一个分组,
// 成员请求数翻倍、日志重复, 冷却与探测结论互相覆盖。
//
// 放在 relay 而不是 HTTP 处理层: 定时探测在 task 侧, 看不到 handlers 包的私有变量;
// 反过来让 task 依赖 handlers 会成环。
var groupTests sync.Map

// GroupTestAcquire 抢占分组测试闸, 成功返回 true; 调用方必须在测试结束后 GroupTestRelease。
// 抢不到表示该分组已有一轮在跑, 调用方应跳过该分组而不是排队等待。
func GroupTestAcquire(groupID int) bool {
	_, loaded := groupTests.LoadOrStore(groupID, struct{}{})
	return !loaded
}

// GroupTestRelease 释放分组测试闸。
func GroupTestRelease(groupID int) {
	groupTests.Delete(groupID)
}
