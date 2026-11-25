package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newClaims(issuedAgo, remaining time.Duration) *jwt.RegisteredClaims {
	now := time.Now()
	return &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-issuedAgo)),
		ExpiresAt: jwt.NewNumericDate(now.Add(remaining)),
	}
}

func TestRenewalExpire(t *testing.T) {
	const day = 24 * time.Hour

	cases := []struct {
		name   string
		claims *jwt.RegisteredClaims
		wantOK bool
		want   int
	}{
		{
			name:   "刚签发的 30 天凭据不续期",
			claims: newClaims(0, 30*day),
			wantOK: false,
		},
		{
			name:   "30 天凭据用掉 20 天后续期",
			claims: newClaims(20*day, 10*day),
			wantOK: true,
			want:   int((30 * day).Seconds()),
		},
		{
			name:   "1 天凭据只剩 4 小时后续期",
			claims: newClaims(20*time.Hour, 4*time.Hour),
			wantOK: true,
			want:   int(day.Seconds()),
		},
		{
			name:   "1 天凭据刚开始用不续期",
			claims: newClaims(time.Hour, 23*time.Hour),
			wantOK: false,
		},
		{
			name:   "缺少签发时间不续期",
			claims: &jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))},
			wantOK: false,
		},
		{
			name:   "声明为空不续期",
			claims: nil,
			wantOK: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := RenewalExpire(c.claims)
			if ok != c.wantOK {
				t.Fatalf("RenewalExpire() ok = %v, want %v", ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Fatalf("RenewalExpire() expire = %d, want %d", got, c.want)
			}
		})
	}
}
