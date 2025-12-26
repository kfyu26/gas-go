package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	jwtSecretKey     = "gas-monitor-jwt-secret-change-in-production"
	tokenExpiryHours = 24
)

type Claims struct {
	jwt.RegisteredClaims
}

type AuthConfig struct {
	Enabled       bool   `json:"enabled"`
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"` // bcrypt 加密后的密码
}

// 检查是否已启用认证
func isAuthEnabled(store *Store) (bool, error) {
	enabled, err := store.GetSetting("auth_enabled", "false")
	if err != nil {
		return false, err
	}
	return parseBoolSetting(enabled, false), nil
}

// 检查是否已配置管理员密码
func isAdminConfigured(store *Store) (bool, error) {
	password, err := store.GetSetting("admin_password", "")
	if err != nil {
		return false, err
	}
	return password != "", nil
}

// 初始化管理员账号
func InitAdmin(store *Store, username, password string) error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if err := store.SetSetting("admin_username", username); err != nil {
		return err
	}
	if err := store.SetSetting("admin_password", string(hashedPassword)); err != nil {
		return err
	}
	if err := store.SetSetting("auth_enabled", "true"); err != nil {
		return err
	}
	return nil
}

// 校验管理员密码
func VerifyAdminPassword(store *Store, password string) (bool, error) {
	hashedPassword, err := store.GetSetting("admin_password", "")
	if err != nil {
		return false, err
	}
	if hashedPassword == "" {
		return false, nil // 尚未配置管理员
	}

	err = bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil, nil
}

// 生成 JWT Token
func GenerateToken(store *Store) (string, error) {
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * tokenExpiryHours)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   "admin",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(jwtSecretKey))
}

// 校验 JWT Token
func ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(jwtSecretKey), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}

// 生成安全的随机密钥
func generateSecureKey(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// JWT 认证中间件
func AuthMiddleware(store *Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enabled, err := isAuthEnabled(store)
			if err != nil {
				http.Error(w, `{"error":"认证检查失败"}`, http.StatusInternalServerError)
				return
			}

			if !enabled {
				next.ServeHTTP(w, r)
				return
			}

			publicExact := map[string]struct{}{
				"/":                {},
				"/login":           {},
				"/api/login":       {},
				"/api/auth/status": {},
				"/api/metrics":     {},
				"/api/hourly":      {},
				"/api/monthly":     {},
				"/api/recent":      {},
				"/favicon.ico":     {},
			}
			publicPrefixes := []string{"/static/"}

			if _, ok := publicExact[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			for _, prefix := range publicPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":"未登录，请先登录"}`, http.StatusUnauthorized)
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				http.Error(w, `{"error":"无效的认证格式"}`, http.StatusUnauthorized)
				return
			}

			_, err = ValidateToken(parts[1])
			if err != nil {
				http.Error(w, `{"error":"Token 无效或已过期"}`, http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
