// Package auth — JWT/Session 令牌管理
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TokenClaims 令牌载荷
type TokenClaims struct {
	Site      string `json:"site"`
	Username  string `json:"username"`
	SessionID string `json:"session_id"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// TokenManager 会话令牌管理器
type TokenManager struct {
	mu       sync.RWMutex
	tokens   map[string]*TokenClaims // sessionID → claims
	secret   []byte
	lifetime time.Duration
}

// NewTokenManager 创建令牌管理器
func NewTokenManager(secret string, lifetime time.Duration) *TokenManager {
	return &TokenManager{
		tokens:   make(map[string]*TokenClaims),
		secret:   []byte(secret),
		lifetime: lifetime,
	}
}

// Issue 签发会话令牌
func (tm *TokenManager) Issue(site, username string, sessionID string) string {
	now := time.Now().Unix()
	claims := TokenClaims{
		Site:      site,
		Username:  username,
		SessionID: sessionID,
		IssuedAt:  now,
		ExpiresAt: now + int64(tm.lifetime.Seconds()),
	}
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	sig := tm.sign(encoded)
	token := fmt.Sprintf("%s.%s", encoded, sig)

	tm.mu.Lock()
	tm.tokens[sessionID] = &claims
	tm.mu.Unlock()

	return token
}

// Verify 验证令牌有效性
func (tm *TokenManager) Verify(token string) (*TokenClaims, bool) {
	parts := splitToken(token)
	if len(parts) != 2 {
		return nil, false
	}

	if tm.sign(parts[0]) != parts[1] {
		return nil, false
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}

	claims, err := parseClaims(payload)
	if err != nil {
		return nil, false
	}

	if time.Now().Unix() > claims.ExpiresAt {
		return nil, false
	}

	tm.mu.RLock()
	_, exists := tm.tokens[claims.SessionID]
	tm.mu.RUnlock()

	return claims, exists
}

// Revoke 撤销令牌
func (tm *TokenManager) Revoke(sessionID string) {
	tm.mu.Lock()
	delete(tm.tokens, sessionID)
	tm.mu.Unlock()
}

// ActiveSessions 活跃会话数
func (tm *TokenManager) ActiveSessions() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return len(tm.tokens)
}

// CleanupExpired 清理过期令牌
func (tm *TokenManager) CleanupExpired() int {
	now := time.Now().Unix()
	removed := 0
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for id, c := range tm.tokens {
		if now > c.ExpiresAt {
			delete(tm.tokens, id)
			removed++
		}
	}
	return removed
}

func (tm *TokenManager) sign(data string) string {
	mac := hmac.New(sha256.New, tm.secret)
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func splitToken(token string) []string {
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			return []string{token[:i], token[i+1:]}
		}
	}
	return nil
}

func parseClaims(data []byte) (*TokenClaims, error) {
	var c TokenClaims
	err := json.Unmarshal(data, &c)
	return &c, err
}
