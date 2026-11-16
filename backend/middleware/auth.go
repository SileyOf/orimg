package middleware

import (
	"net/http"
	"strings"

	"zhuapic/config"
	"zhuapic/utils"
)

func ClaimsFromRequest(r *http.Request, cfg *config.Config) (*utils.Claims, bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return nil, false
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return nil, false
	}
	claims, err := utils.ParseToken(cfg, parts[1])
	if err != nil {
		return nil, false
	}
	return claims, true
}
