package model

import (
	"fmt"
	"net/url"
	"strconv"
)

type SettingKey string

const (
	SettingKeyProxyURL                SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval       SettingKey = "stats_save_interval"        // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval SettingKey = "model_info_update_interval" // 模型信息更新间隔(小时)
	SettingKeyCORSAllowOrigins        SettingKey = "cors_allow_origins"         // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyHealthCheckInterval     SettingKey = "health_check_interval"      // 渠道健康检查周期(分钟), 0 表示关闭
	SettingKeyHealthFailThreshold     SettingKey = "health_fail_threshold"      // 渠道连续健康检查失败多少次后自动禁用
	SettingKeyRelayLogKeepEnabled     SettingKey = "relay_log_keep_enabled"     // 是否保留历史转发日志(关闭时仅内存保留最近若干条)
	SettingKeyRelayLogKeepPeriod      SettingKey = "relay_log_keep_period"      // 历史日志保留天数, 超期由周期任务清理
)

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
		{Key: SettingKeyStatsSaveInterval, Value: "10"},       // 默认10分钟保存一次统计信息
		{Key: SettingKeyCORSAllowOrigins, Value: ""},          // CORS 默认不允许跨域，设置为 "*" 才允许所有来源
		{Key: SettingKeyModelInfoUpdateInterval, Value: "24"}, // 默认24小时更新一次模型信息
		{Key: SettingKeyHealthCheckInterval, Value: "30"},     // 默认30分钟健康检查一次渠道, 0 表示关闭
		{Key: SettingKeyHealthFailThreshold, Value: "3"},      // 默认连续失败3次自动禁用渠道
		{Key: SettingKeyRelayLogKeepEnabled, Value: "true"},   // 默认保留历史日志
		{Key: SettingKeyRelayLogKeepPeriod, Value: "7"},       // 默认日志保存7天
	}
}

func (s *Setting) Validate() error {
	switch s.Key {
	case SettingKeyModelInfoUpdateInterval:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("model info update interval must be an integer")
		}
		return nil
	case SettingKeyHealthCheckInterval:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("health check interval must be an integer")
		}
		return nil
	case SettingKeyHealthFailThreshold:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("health fail threshold must be an integer")
		}
		return nil
	case SettingKeyRelayLogKeepPeriod:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("relay log keep period must be an integer")
		}
		return nil
	case SettingKeyRelayLogKeepEnabled:
		if s.Value != "true" && s.Value != "false" {
			return fmt.Errorf("relay log keep enabled must be true or false")
		}
		return nil
	case SettingKeyProxyURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("proxy URL is invalid: %w", err)
		}
		validSchemes := map[string]bool{
			"http":    true,
			"https":   true,
			"socks5":  true,
			"socks5h": true,
		}
		if !validSchemes[parsedURL.Scheme] {
			return fmt.Errorf("proxy URL scheme must be http, https, socks5, or socks5h")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("proxy URL must have a host")
		}
		return nil
	}

	return nil
}
