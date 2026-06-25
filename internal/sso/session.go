package sso

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/auth"
	"github.com/rise197/vpn-heartbeat-skill/internal/config"
	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
)

// SessionState 会话状态
type SessionState int

const (
	StateIdle          SessionState = iota // 未连接
	StateAuthenticating                     // 认证中
	StateActive                            // 活跃
	StateExpired                           // 已过期
	StateFailed                            // 失败
)

func (s SessionState) String() string {
	switch s {
	case StateIdle: return "空闲"
	case StateAuthenticating: return "认证中"
	case StateActive: return "活跃"
	case StateExpired: return "过期"
	case StateFailed: return "失败"
	default: return "未知"
	}
}

// Session 单个业务站点的会话
type Session struct {
	Site      config.Site
	State     SessionState
	Token     string
	SSO       *Endpoint   // 认证时使用的 SSO 节点
	CreatedAt time.Time
	ExpiresAt time.Time
	LastPing  time.Time
	mu        sync.RWMutex
}

// Pool 会话池 — 管理所有业务站点的认证会话
type Pool struct {
	mu       sync.RWMutex
	sessions map[string]*Session // site.Name → session
	tm       *auth.TokenManager
	client   *Client
	log      *logger.Logger
}

// NewPool 创建会话池
func NewPool(client *Client, tm *auth.TokenManager) *Pool {
	return &Pool{
		sessions: make(map[string]*Session),
		tm:       tm,
		client:   client,
		log:      logger.New("session-pool"),
	}
}

// Login 为一个业务站点建立 SSO 认证会话
// 先用 SSO 的全局凭证登录，成功后签发站点专用 token
func (p *Pool) Login(site config.Site) (*Session, error) {
	p.mu.Lock()
	// 检查是否已有活跃会话
	if existing, ok := p.sessions[site.Name]; ok {
		existing.mu.RLock()
		active := existing.State == StateActive
		existing.mu.RUnlock()
		if active {
			p.mu.Unlock()
			p.log.Info("站点 [%s] 已有活跃会话，跳过", site.Name)
			return existing, nil
		}
	}
	p.mu.Unlock()

	p.log.Info("正在为 [%s] 建立 SSO 会话...", site.Name)
	sessionID := generateSessionID()

	// SSO 认证
	resp, ep, err := p.client.Authenticate(site.Username, site.Password)
	if err != nil {
		p.recordSession(site, StateFailed, "", nil)
		return nil, fmt.Errorf("站点 [%s] SSO 认证失败: %w", site.Name, err)
	}

	// 签发站点令牌
	token := p.tm.Issue(site.Name, site.Username, sessionID)

	sess := &Session{
		Site:      site,
		State:     StateActive,
		Token:     token,
		SSO:       ep,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(8 * time.Hour),
		LastPing:  time.Now(),
	}

	p.mu.Lock()
	p.sessions[site.Name] = sess
	p.mu.Unlock()

	p.log.Info("✓ [%s] 会话建立成功 (via %s) response=%v", site.Name, ep.Name, resp)
	return sess, nil
}

// LoginAll 为所有业务站点建立 SSO 会话
func (p *Pool) LoginAll(sites []config.Site) map[string]*Session {
	results := make(map[string]*Session)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := range sites {
		wg.Add(1)
		go func(s config.Site) {
			defer wg.Done()
			sess, err := p.Login(s)
			mu.Lock()
			if err != nil {
				p.log.Error("✗ [%s] 登录失败: %v", s.Name, err)
				sess, _ = p.Get(s.Name) // 返回失败的 session
			}
			results[s.Name] = sess
			mu.Unlock()
		}(sites[i])
	}

	wg.Wait()
	return results
}

// Get 获取指定站点的会话
func (p *Pool) Get(siteName string) (*Session, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s, ok := p.sessions[siteName]
	return s, ok
}

// List 列出所有会话状态
func (p *Pool) List() []*Session {
	p.mu.RLock()
	defer p.mu.RUnlock()
	list := make([]*Session, 0, len(p.sessions))
	for _, s := range p.sessions {
		list = append(list, s)
	}
	return list
}

// ActiveCount 活跃会话数
func (p *Pool) ActiveCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for _, s := range p.sessions {
		s.mu.RLock()
		if s.State == StateActive {
			count++
		}
		s.mu.RUnlock()
	}
	return count
}

// ExpireOld 标记过期会话
func (p *Pool) ExpireOld() int {
	now := time.Now()
	expired := 0
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, s := range p.sessions {
		s.mu.Lock()
		if s.State == StateActive && now.After(s.ExpiresAt) {
			s.State = StateExpired
			expired++
		}
		s.mu.Unlock()
		_ = name
	}
	return expired
}

// Stats 返回会话池统计信息
func (p *Pool) Stats() map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stats := map[string]int{"total": len(p.sessions)}
	for _, s := range p.sessions {
		stats[s.State.String()]++
	}
	stats["token_active"] = p.tm.ActiveSessions()
	return stats
}

func (p *Pool) recordSession(site config.Site, state SessionState, token string, ep *Endpoint) {
	sess := &Session{
		Site:      site,
		State:     state,
		Token:     token,
		SSO:       ep,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(8 * time.Hour),
	}
	p.mu.Lock()
	p.sessions[site.Name] = sess
	p.mu.Unlock()
}

func generateSessionID() string {
	return fmt.Sprintf("sess-%s-%04d", time.Now().Format("20060102150405"), rand.Intn(9999))
}
