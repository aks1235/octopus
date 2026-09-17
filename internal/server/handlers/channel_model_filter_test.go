package handlers

import (
	"testing"

	regexp2 "github.com/dlclark/regexp2"
)

// compileFilter 测试辅助: 编译失败直接 Fatal, 用例里不掺错误分支噪音。
func compileFilter(t *testing.T, pattern string) *regexp2.Regexp {
	t.Helper()
	if pattern == "" {
		return nil
	}
	re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		t.Fatalf("compile %q: %v", pattern, err)
	}
	return re
}

// TestModelNameKept 锁定两层模型过滤的命中语义(2026-09-17 由"命中保留"改为全局"命中排除"):
// 渠道级 match_regex = 白名单(命中保留), 全局 model_filter = 黑名单(命中排除), 两侧 AND 叠加。
func TestModelNameKept(t *testing.T) {
	tests := []struct {
		name     string
		channel  string // 渠道级 match_regex, 空串表示未配置
		global   string // 全局 model_filter, 空串表示未配置
		model    string
		wantKept bool
	}{
		{
			name:     "global hit excludes model",
			channel:  "",
			global:   "embedding|rerank",
			model:    "text-embedding-3-small",
			wantKept: false,
		},
		{
			name:     "global miss keeps model",
			channel:  "",
			global:   "embedding|rerank",
			model:    "glm-5.3",
			wantKept: true,
		},
		{
			name:     "empty global filters nothing",
			channel:  "",
			global:   "",
			model:    "text-embedding-3-small",
			wantKept: true,
		},
		{
			name:     "channel whitelist keeps only hits",
			channel:  "^glm-",
			global:   "",
			model:    "gpt-4o",
			wantKept: false,
		},
		{
			name:     "both layers intersect whitelist and non-blacklist",
			channel:  "^glm-",
			global:   "embedding|rerank",
			model:    "glm-embedding",
			wantKept: false, // 通过渠道白名单但被全局黑名单命中
		},
		{
			name:     "both layers keep model passing both",
			channel:  "^glm-",
			global:   "embedding|rerank",
			model:    "glm-5.3",
			wantKept: true,
		},
		{
			name:     "both layers drop model outside whitelist even if not blacklisted",
			channel:  "^glm-",
			global:   "embedding|rerank",
			model:    "gpt-4o",
			wantKept: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := modelNameKept(compileFilter(t, tt.channel), compileFilter(t, tt.global), tt.model)
			if err != nil {
				t.Fatalf("modelNameKept(%q): %v", tt.model, err)
			}
			if got != tt.wantKept {
				t.Fatalf("modelNameKept(channel=%q, global=%q, model=%q) = %v, want %v",
					tt.channel, tt.global, tt.model, got, tt.wantKept)
			}
		})
	}
}
