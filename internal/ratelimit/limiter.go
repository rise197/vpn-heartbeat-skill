// Package ratelimit — 令牌桶速率限制器
// 保护业务站点不被过度访问，支持按站点和全局两级限流。
package ratelimit

import (
	"sync"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
)

// Limiter 令牌桶速率限制器
// 以恒定速率生成令牌，每个请求消耗一个令牌
type Limiter struct {
	name      string
	rate      float64 // 令牌生成速率（个/秒）
	burst     int     // 桶容量（允许的突发请求数）
	tokens    float64 // 当前令牌数
	lastRefill time.Time
	mu        sync.Mutex
	log       *logger.Logger
}

// New 创建一个新的速率限制器
// rate: 每秒生成的令牌数
// burst: 桶的最大容量
func New(name string, rate float64, burst int) *Limiter {
	return &Limiter{
		name:   name,
		rate:   rate,
		burst:  burst,
		tokens: float64(burst), // 初始满桶
		log:    logger.New("ratelimit:" + name),
	}
}

// Allow 检查是否允许一个请求通过
// 返回 true 表示放行，false 表示被限流
func (l *Limiter) Allow() bool {
	return l.AllowN(1)
}

// AllowN 检查是否允许 N 个请求通过
func (l *Limiter) AllowN(n int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()

	if l.tokens >= float64(n) {
		l.tokens -= float64(n)
		return true
	}

	return false
}

// Wait 阻塞等待直到获取到令牌或超时
func (l *Limiter) Wait(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if l.Allow() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Tokens 返回当前可用令牌数（调试用）
func (l *Limiter) Tokens() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill()
	return l.tokens
}

// refill 按时间推进补充令牌（内部方法，调用前需持有锁）
func (l *Limiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	// 补充令牌
	l.tokens += elapsed * l.rate
	if l.tokens > float64(l.burst) {
		l.tokens = float64(l.burst)
	}
	l.lastRefill = now
}

// Pool 多站点速率限制器池
// 每个站点持有独立的 Limiter，全局还有总限流
type Pool struct {
	limiters map[string]*Limiter
	global   *Limiter
	mu       sync.RWMutex
	log      *logger.Logger
}

// NewPool 创建速率限制器池
// globalRate: 全局每秒允许的总请求数
// perSiteRate: 每个站点每秒允许的请求数
func NewPool(globalRate, perSiteRate float64) *Pool {
	return &Pool{
		limiters: make(map[string]*Limiter),
		global:   New("global", globalRate, int(globalRate*2)),
		log:      logger.New("ratelimit:pool"),
	}
}

// Allow 检查指定站点和全局是否都允许此次请求
func (p *Pool) Allow(siteName string) bool {
	// 先检查全局限制
	if !p.global.Allow() {
		p.log.Debug("全局限流触发")
		return false
	}

	p.mu.RLock()
	lim, ok := p.limiters[siteName]
	p.mu.RUnlock()

	if !ok {
		// 首次访问的站点自动创建 Limiter
		p.mu.Lock()
		lim = New(siteName, 5.0, 10) // 默认 5 req/s
		p.limiters[siteName] = lim
		p.mu.Unlock()
	}

	if !lim.Allow() {
		p.log.Debug("站点 [%s] 限流触发", siteName)
		return false
	}

	return true
}

// Stats 返回所有站点的限流统计
func (p *Pool) Stats() map[string]float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]float64, len(p.limiters))
	for name, lim := range p.limiters {
		result[name] = lim.Tokens()
	}
	result["_global"] = p.global.Tokens()
	return result
}
