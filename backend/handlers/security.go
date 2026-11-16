package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"zhuapic/middleware"
	"zhuapic/models"
	"zhuapic/utils"
)

type rateBucket struct {
	window time.Time
	count  int
}

type ipRateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]rateBucket
	lastSweep time.Time
}

func newIPRateLimiter() *ipRateLimiter {
	return &ipRateLimiter{buckets: map[string]rateBucket{}}
}

func (l *ipRateLimiter) Allow(key string, maxPerMinute int) bool {
	if maxPerMinute <= 0 {
		return true
	}
	now := time.Now()
	minute := now.Truncate(time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastSweep.Before(minute) {
		cutoff := minute.Add(-2 * time.Minute)
		for bucketKey, existing := range l.buckets {
			if existing.window.Before(cutoff) {
				delete(l.buckets, bucketKey)
			}
		}
		l.lastSweep = minute
	}
	bucket := l.buckets[key]
	if !bucket.window.Equal(minute) {
		bucket = rateBucket{window: minute}
	}
	bucket.count++
	l.buckets[key] = bucket
	return bucket.count <= maxPerMinute
}

var (
	loginLimiter  = newIPRateLimiter()
	uploadLimiter = newIPRateLimiter()
	imageLimiter  = newIPRateLimiter()
)

func (a *App) currentUser(r *http.Request) (*models.User, bool) {
	claims, ok := middleware.ClaimsFromRequest(r, a.Cfg)
	if !ok {
		return nil, false
	}
	user, err := a.findUserByID(claims.UserID)
	if err != nil || !user.Active || claims.Version != user.AuthVersion {
		return nil, false
	}
	return user, true
}

func (a *App) RequireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.currentUser(r); !ok {
			utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (a *App) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := a.currentUser(r)
		if !ok {
			utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if user.Role != "admin" {
			utils.WriteError(w, http.StatusForbidden, "admin required")
			return
		}
		next(w, r)
	}
}

type captchaPayload struct {
	Nonce string `json:"nonce"`
	Exp   int64  `json:"exp"`
	Hash  string `json:"hash"`
}

func (a *App) Captcha(w http.ResponseWriter, r *http.Request) {
	left := rand.Intn(8) + 2
	right := rand.Intn(8) + 2
	answer := strconv.Itoa(left + right)
	exp := time.Now().Add(5 * time.Minute).Unix()
	nonce, err := utils.RandomHex(8)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create captcha")
		return
	}
	payload := captchaPayload{
		Nonce: nonce,
		Exp:   exp,
		Hash:  captchaHash(a.Cfg.SigningKey(), nonce, exp, answer),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create captcha")
		return
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	token := encoded + "." + captchaSign(a.Cfg.SigningKey(), encoded)
	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"question": fmt.Sprintf("%d + %d = ?", left, right),
		"token":    token,
	})
}

func (a *App) verifyCaptcha(token string, answer string) error {
	token = strings.TrimSpace(token)
	answer = strings.TrimSpace(answer)
	parts := strings.Split(token, ".")
	if len(parts) != 2 || answer == "" {
		return errors.New("invalid captcha")
	}
	expected := captchaSign(a.Cfg.SigningKey(), parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return errors.New("invalid captcha")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	var payload captchaPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	if payload.Exp < time.Now().Unix() {
		return errors.New("captcha expired")
	}
	if !hmac.Equal([]byte(payload.Hash), []byte(captchaHash(a.Cfg.SigningKey(), payload.Nonce, payload.Exp, answer))) {
		return errors.New("captcha mismatch")
	}
	return nil
}

func captchaHash(secret, nonce string, exp int64, answer string) string {
	return hmacHex(secret, fmt.Sprintf("%s:%d:%s", nonce, exp, answer))
}

func captchaSign(secret string, body string) string {
	return hmacHex(secret, body)
}

func hmacHex(secret string, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
