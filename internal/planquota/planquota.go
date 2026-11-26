// Package planquota 编排订阅套餐额度的采集: 取渠道配置、选厂商实现、落库。
//
// 独立成包而不放进 op, 是因为取 HTTP 客户端要读应用代理设置, 而 rhttp 依赖 op:
// 编排逻辑若放在 op 里, op → rhttp → op 直接成环。
package planquota

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/quota"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// VendorVolcengine 是火山方舟的厂商标识, 与渠道配置里的 plan_quota_vendor 对应。
const VendorVolcengine = "volcengine"

// providers 是已支持的厂商实现。新增一家只需在此登记, 渠道配置改厂商标识即可生效。
var providers = map[string]quota.Provider{
	VendorVolcengine: quota.VolcengineProvider{},
}

// refreshLocks 让同一渠道的采集串行: 定时任务与手动刷新可能同时打到同一渠道,
// 两个请求都带签名与配额消耗, 并发没有收益。
var refreshLocks sync.Map // map[int]*sync.Mutex

func lockFor(channelID int) *sync.Mutex {
	actual, _ := refreshLocks.LoadOrStore(channelID, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// Get 返回渠道最近一次采集结果; 从未采集过时返回 nil 而非错误。
func Get(channelID int) (*model.ChannelPlanQuota, error) {
	var row model.ChannelPlanQuota
	err := db.GetDB().Where("channel_id = ?", channelID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Item 是列表视图的一条: 额度快照外加渠道名与启停, 供首页一屏看完所有套餐。
type Item struct {
	model.ChannelPlanQuota
	ChannelName string `json:"channel_name"`
	Enabled     bool   `json:"enabled"`
}

// List 返回全部已采集到的套餐额度, 按渠道主键排序。
// 渠道删除后其额度行可能残留, 取不到渠道即跳过, 不让孤儿行出现在首页。
func List() ([]Item, error) {
	var rows []model.ChannelPlanQuota
	if err := db.GetDB().Order("channel_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		channel, err := op.ChannelGet(row.ChannelID)
		if err != nil {
			continue
		}
		items = append(items, Item{
			ChannelPlanQuota: row,
			ChannelName:      channel.Name,
			Enabled:          channel.Enabled,
		})
	}
	return items, nil
}

// Refresh 采集一个渠道的套餐额度并落库。
//
// 采集失败不清空上次的成功数据: 网络抖动或临时 5xx 时把面板清空, 比显示一份略旧的数据更误导。
// 失败只更新 error 与 fetched_at, windows 与 synced_at 保持上一次成功的值, 由界面提示数据可能已过期。
func Refresh(ctx context.Context, channel model.Channel) error {
	mu := lockFor(channel.ID)
	mu.Lock()
	defer mu.Unlock()

	if channel.PlanQuotaVendor == "" {
		return nil
	}
	provider, ok := providers[channel.PlanQuotaVendor]
	if !ok {
		return saveFailure(channel, fmt.Errorf("unsupported plan quota vendor %q", channel.PlanQuotaVendor))
	}

	client, release, err := httpClientFor(channel)
	if err != nil {
		return saveFailure(channel, err)
	}
	defer release()

	cred := quota.Credential{
		AccessKeyID:     channel.PlanQuotaAccessKeyID,
		SecretAccessKey: channel.PlanQuotaSecretAccessKey,
		Kind:            planKind(channel.PlanQuotaKind),
	}
	snapshot, err := provider.Fetch(ctx, client, cred)
	if err != nil {
		return saveFailure(channel, err)
	}
	return saveSuccess(channel, snapshot)
}

// planKind 把渠道配置里的套餐类型翻成 quota 包的常量; 留空按 Agent Plan 处理,
// 因为当前只实现了这一种, 让用户少填一个字段。
func planKind(raw string) string {
	if raw == "" {
		return quota.KindAgentPlan
	}
	return raw
}

// httpClientFor 按渠道的代理配置取客户端, 与转发、探测两处的取法一致。
// 返回的 release 只在客户端是本次新建时需要释放其空闲连接。
func httpClientFor(channel model.Channel) (*http.Client, func(), error) {
	if !channel.Proxy {
		client, err := rhttp.Direct()
		return client, func() {}, err
	}
	if channel.ChannelProxy == "" {
		client, err := rhttp.Proxy()
		return client, func() {}, err
	}
	client, err := rhttp.New(channel.ChannelProxy)
	if err != nil {
		return nil, func() {}, err
	}
	// 渠道专用代理的客户端是本次新建的, 用完必须关掉空闲连接, 否则会随丢弃的 Transport 残留到超时。
	return client, client.CloseIdleConnections, nil
}

func saveSuccess(channel model.Channel, snapshot *quota.Snapshot) error {
	now := time.Now().Unix()
	windows := make([]model.PlanQuotaWindow, 0, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		windows = append(windows, model.PlanQuotaWindow{
			Key:     string(window.Key),
			Used:    window.Used,
			Total:   window.Total,
			Percent: window.Percent,
			ResetAt: window.ResetAt,
		})
	}

	row := model.ChannelPlanQuota{
		ChannelID: channel.ID,
		Vendor:    channel.PlanQuotaVendor,
		Kind:      channel.PlanQuotaKind,
		PlanName:  snapshot.PlanName,
		FetchedAt: now,
		SyncedAt:  now,
		Error:     "",
		Windows:   windows,
	}
	return db.GetDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "channel_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"vendor", "kind", "plan_name", "fetched_at", "synced_at", "error", "windows",
		}),
	}).Create(&row).Error
}

func saveFailure(channel model.Channel, cause error) error {
	now := time.Now().Unix()
	message := cause.Error()
	if len(message) > 500 {
		message = message[:500]
	}

	row := model.ChannelPlanQuota{
		ChannelID: channel.ID,
		Vendor:    channel.PlanQuotaVendor,
		Kind:      channel.PlanQuotaKind,
		FetchedAt: now,
		Error:     message,
	}
	// 只覆盖这两列: 上一次成功的 windows 与 synced_at 必须留着, 界面据此区分"没配过"和"这次没刷上"。
	if err := db.GetDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "channel_id"}},
		DoUpdates: clause.Assignments(map[string]any{"fetched_at": now, "error": message}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("%w (also failed to record the error: %v)", cause, err)
	}
	return cause
}

// SyncAll 采集全部配置了额度的渠道。单渠道失败不影响其他渠道, 错误已落库并记一条告警日志。
func SyncAll(ctx context.Context) {
	targets := op.ChannelPlanQuotaTargets()
	if len(targets) == 0 {
		return
	}
	for _, channel := range targets {
		if err := Refresh(ctx, channel); err != nil {
			log.Warnf("plan quota sync failed, channel %d (%s): %v", channel.ID, channel.Name, err)
		}
	}
}
