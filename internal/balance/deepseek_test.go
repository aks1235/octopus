package balance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsOfficialDeepSeekBaseURL(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://api.deepseek.com", true},
		{"https://api.deepseek.com/v1", true},
		{"  https://api.deepseek.com/v1/chat/completions  ", true},
		{"http://API.DEEPSEEK.COM", true},
		{"https://api.fengwind.com", false},
		{"https://api.deepseek.com.evil.example", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsOfficialDeepSeekBaseURL(tc.raw); got != tc.want {
			t.Errorf("IsOfficialDeepSeekBaseURL(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestFetch 用本地服务器验证完整链路: 请求头、字符串金额解析、可用标志。
func TestFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"is_available": true,
			"balance_infos": [{
				"currency": "CNY",
				"total_balance": "110.50",
				"granted_balance": "10.50",
				"topped_up_balance": "100.00"
			}]
		}`))
	}))
	defer server.Close()

	original := deepSeekBalanceURL
	deepSeekBalanceURL = server.URL
	defer func() { deepSeekBalanceURL = original }()

	result, err := Fetch(context.Background(), server.Client(), "sk-test")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !result.Available {
		t.Fatal("Available = false, want true")
	}
	if len(result.Entries) != 1 {
		t.Fatalf("Entries length = %d, want 1", len(result.Entries))
	}
	entry := result.Entries[0]
	if entry.Currency != "CNY" || entry.Total != "110.50" || entry.Granted != "10.50" || entry.ToppedUp != "100.00" {
		t.Errorf("unexpected entry: %+v", entry)
	}
}

// TestParseNumericAmount 覆盖文档写 string 但网关返回数字的情况。
func TestParseNumericAmount(t *testing.T) {
	result, err := parseResponse([]byte(`{
		"is_available": false,
		"balance_infos": [{"currency":"USD","total_balance":0,"granted_balance":0,"topped_up_balance":0}]
	}`))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if result.Available {
		t.Fatal("Available = true, want false")
	}
	if got := result.Entries[0].Total; got != "0" {
		t.Errorf("Total = %q, want %q", got, "0")
	}
}

func TestFetchRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer server.Close()

	original := deepSeekBalanceURL
	deepSeekBalanceURL = server.URL
	defer func() { deepSeekBalanceURL = original }()

	if _, err := Fetch(context.Background(), server.Client(), "sk-bad"); err == nil {
		t.Fatal("Fetch succeeded on HTTP 401, want error")
	}
}
