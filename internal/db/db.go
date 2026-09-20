package db

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db/migrate"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

func InitDB(dbType, dsn string, debug bool) error {
	var err error
	gormConfig := gorm.Config{Logger: logger.Discard}
	if debug {
		gormConfig.Logger = logger.Default.LogMode(logger.Info)
	}

	switch dbType {
	case "sqlite":
		db, err = initSQLite(dsn, &gormConfig)
	case "mysql":
		db, err = initMySQL(dsn, &gormConfig)
	case "postgres", "postgresql":
		db, err = initPostgres(dsn, &gormConfig)
	default:
		return fmt.Errorf("unsupported database type: %s", dbType)
	}

	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	if err := migrate.BeforeAutoMigrate(db); err != nil {
		return err
	}
	// AutoMigrate 期间临时关闭外键检查: GORM 重建表时会先创建临时表再复制数据,
	// 旧数据中可能存在尚未迁移的外键引用(如 group_items.channel_grant_id = 0),
	// 开启外键检查会导致 INSERT INTO 临时表失败。关闭后由迁移链保证数据完整性。
	if db.Dialector != nil && db.Dialector.Name() == "sqlite" {
		db.Exec("PRAGMA foreign_keys=OFF")
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.Channel{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
		&model.Group{},
		&model.GroupItem{},
		&model.GroupChannelOrder{},
		&model.LLMInfo{},
		&model.APIKey{},
		&model.RelayLog{},
		&model.Setting{},
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsHourly{},
		&model.StatsChannelDaily{},
		&model.StatsModelDaily{},
		&model.StatsAPIKey{},
		&migrate.MigrationRecord{},
	); err != nil {
		return err
	}
	// 恢复外键检查
	if db.Dialector != nil && db.Dialector.Name() == "sqlite" {
		db.Exec("PRAGMA foreign_keys=ON")
	}
	if err := migrate.AfterAutoMigrate(db); err != nil {
		return err
	}
	// Postgres: schema changes during migrations can invalidate cached prepared plans
	// (e.g. "cached plan must not change result type"). Clear them.
	if db.Dialector != nil && db.Dialector.Name() == "postgres" {
		db.Exec("DEALLOCATE ALL")
		db.Exec("DISCARD ALL")
	}
	return nil
}

// initSQLite 使用指定文件路径初始化 SQLite，并为每个连接应用运行参数。
func initSQLite(path string, config *gorm.Config) (*gorm.DB, error) {
	params := url.Values{}
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "synchronous(NORMAL)")
	params.Add("_pragma", "cache_size(10000)")
	// busy_timeout 提到 10s: IMMEDIATE 事务开锁拿不到时在驱动层排队等待,
	// 吸收转发路径偶发的长写, 减少不必要的重试。
	params.Add("_pragma", "busy_timeout(10000)")
	params.Add("_pragma", "foreign_keys(ON)")
	params.Add("_pragma", "auto_vacuum(INCREMENTAL)")
	params.Add("_pragma", "mmap_size(268435456)")
	params.Add("_pragma", "locking_mode(NORMAL)")
	// 事务默认改用 BEGIN IMMEDIATE, 从根上消灭 SQLITE_BUSY_SNAPSHOT(517):
	// 默认延迟事务 BEGIN 后先读后写, WAL 下读到旧快照后若别的写者已提交,
	// 升级为写时快照即作废并立刻失败, busy_timeout 对它无效(生产日志每
	// 5 分钟刷 "database is locked (517)" 的根因: 兜底大事务 vs 转发路径
	// 持续写入抢写锁)。IMMEDIATE 在开启事务时就拿写锁, 拿不到则走
	// busy_timeout 排队等待, 锁竞争前移但不会更快失败(SQLite 本就单写者);
	// 读路径不受影响(WAL 读不阻塞)。
	params.Add("_txlock", "immediate")
	return gorm.Open(sqlite.Open(path+"?"+params.Encode()), config)
}

func initMySQL(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
	if !strings.Contains(dsn, "?") {
		dsn += "?charset=utf8mb4&parseTime=True&loc=Local"
	}
	return gorm.Open(mysql.Open(dsn), config)
}

func initPostgres(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: host=localhost user=postgres password=xxx dbname=octopus port=5432 sslmode=disable
	return gorm.Open(postgres.Open(dsn), config)
}

func Close() error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func GetDB() *gorm.DB {
	return db
}
