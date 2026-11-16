package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"zhuapic/config"
)

type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Expires  int64  `json:"exp"`
	IssuedAt int64  `json:"iat"`
	Version  uint64 `json:"version,omitempty"`
}

func GenerateToken(cfg *config.Config, userID uint, username string, email string, role string, version ...uint64) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		Email:    email,
		Role:     role,
		Expires:  now.Add(cfg.JWTExpiry).Unix(),
		IssuedAt: now.Unix(),
	}
	headerJSON, err := json.Marshal(header)
	if len(version) > 0 {
		claims.Version = version[0]
	}
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := signHS256(unsigned, cfg.SigningKey())
	return unsigned + "." + signature, nil
}

func ParseToken(cfg *config.Config, tokenString string) (*Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token")
	}
	unsigned := parts[0] + "." + parts[1]
	expected := signHS256(unsigned, cfg.SigningKey())
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, errors.New("invalid token signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims Claims
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, err
	}
	if claims.Expires < time.Now().Unix() {
		return nil, errors.New("token expired")
	}
	return &claims, nil
}

func signHS256(input string, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func UserIDFromString(raw string) (uint, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	return uint(id), err
}
