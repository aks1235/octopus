package db

import (
	"errors"
	"strings"

	gosqlite "github.com/glebarez/go-sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsBusyError 判断 err 是否为 SQLite 锁竞争类错误(BUSY/LOCKED 家族)。
//
// glebarez/go-sqlite 的 Error 结构体字段私有, 只能 errors.As 取出后用
// Code() 读错误码。Code() 返回含扩展位的原始码(如 517 = BUSY_SNAPSHOT,
// 即 WAL 延迟事务快照作废), 低 8 位是主错误码(5 = BUSY, 6 = LOCKED),
// 按主码归类即可覆盖整个家族。字符串兜底覆盖错误被中间层包装后类型
// 丢失的情况(驱动 BUSY 文案固定为 "database is locked (5xx)")。
func IsBusyError(err error) bool {
	if err == nil {
		return false
	}
	var sqlErr *gosqlite.Error
	if errors.As(err, &sqlErr) {
		code := sqlErr.Code() & 0xff
		return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
	}
	return strings.Contains(err.Error(), "database is locked") ||
		strings.Contains(err.Error(), "database file is locked") ||
		// SQLITE_LOCKED 家族的驱动文案是 "database table is locked", 类型丢失时同样兜住。
		strings.Contains(err.Error(), "table is locked")
}
