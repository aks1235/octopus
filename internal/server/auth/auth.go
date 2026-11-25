package auth

import (
	"crypto/rand"
	"math/big"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/golang-jwt/jwt/v5"
)

func GenerateJWTToken(expiresSec int) (string, int, error) {
	now := time.Now()
	maxAge := int((15 * time.Minute).Seconds())
	if expiresSec > 0 {
		maxAge = expiresSec
	} else if expiresSec == -1 {
		maxAge = int((30 * 24 * time.Hour).Seconds())
	}
	claims := &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Issuer:    conf.APP_NAME,
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(maxAge) * time.Second)),
	}
	user := op.UserGet()
	secret := user.Username + user.Password
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		return "", 0, err
	}
	return token, maxAge, nil
}

func VerifyJWTToken(token string) bool {
	_, ok := ParseTokenClaims(token)
	return ok
}

// ParseTokenClaims 校验 token 并返回声明，口径与 VerifyJWTToken 一致。
// 额外暴露 iat/exp，供中间件判断是否需要滑动续期。
func ParseTokenClaims(token string) (*jwt.RegisteredClaims, bool) {
	claims := &jwt.RegisteredClaims{}
	jwtToken, err := jwt.ParseWithClaims(token, claims,
		func(token *jwt.Token) (interface{}, error) {
			user := op.UserGet()
			secret := user.Username + user.Password
			return []byte(secret), nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil || !jwtToken.Valid {
		return nil, false
	}
	return claims, true
}

// RenewalExpire 判断是否需要滑动续期：当剩余有效期不足签发时长的一半时，
// 返回原始签发时长（秒），由调用方用同一时长重发 cookie。
//
// 这样「信任此设备」的 30 天凭据只要还在使用就不会被固定到期日硬踢下线，
// 停止使用后仍然会在最后一刻失效。
func RenewalExpire(claims *jwt.RegisteredClaims) (int, bool) {
	if claims == nil || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return 0, false
	}
	total := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	if total <= 0 {
		return 0, false
	}
	if time.Until(claims.ExpiresAt.Time) > total/2 {
		return 0, false
	}
	return int(total.Seconds()), true
}

func GenerateAPIKey() string {
	const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 48)
	maxI := big.NewInt(int64(len(keyChars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, maxI)
		if err != nil {
			return ""
		}
		b[i] = keyChars[n.Int64()]
	}
	return "sk-" + conf.APP_NAME + "-" + string(b)
}
