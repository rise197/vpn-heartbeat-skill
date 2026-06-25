// Package heartbeat — VPN 心跳引擎，维持隧道存活 + 站点健康监控
package heartbeat

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/config"
	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
	"github.com/rise197/vpn-heartbeat-skill/internal/sso"
)

// Status 心跳状态
type Status struct {
	Running     bool              `json:"running"`
	StartTime   time.Time         `json:"start_time"`
	Heartbeats  int64             `json:"heartbeats"`
	Failures    int64             `json:"failures"`
	LastSuccess time.Time         `json:"last_success"`
	SiteHealth  map[string]string `json:"site_health"` // siteName → "ok"/"fail"
}

// Engine VPN 心跳引擎
type Engine struct {
	cfg       config.HeartbeatConfig
	client    *http.Client
	pool      *sso.Pool
	ssoClient *sso.Client
	sites     []config.Site
	log       *logger.Logger

	mu      sync.RWMutex
	status  Status
	ctx     context.Context
	cancel  context.CancelFunc
}

// New 创建心跳引擎
func New(cfg config.HeartbeatConfig, pool *sso.Pool, ssoClient *sso.Client, sites []config.Site) *Engine {
	tr := &http.Transport{
		MaxIdleConns:    20,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Engine{
		cfg:       cfg,
		client:    &http.Client{Transport: tr, Timeout: time.Duration(cfg.TimeoutSec) * time.Second},
		pool:      pool,
		ssoClient: ssoClient,
		sites:     sites,
		log:       logger.New("heartbeat"),
		status: Status{
			SiteHealth: make(map[string]string),
		},
	}
}

// Start 启动心跳循环
func (e *Engine) Start() {
	e.ctx, e.cancel = context.WithCancel(context.Background())

	e.mu.Lock()
	e.status.Running = true
	e.status.StartTime = time.Now()
	e.mu.Unlock()

	e.log.Info("心跳引擎启动 (间隔=%ds, 超时=%ds, 重试=%d)",
		e.cfg.IntervalSec, e.cfg.TimeoutSec, e.cfg.RetryMax)

	// 启动时立即执行一次
	e.beat()

	go e.loop()
}

// Stop 停止心跳循环
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Lock()
	e.status.Running = false
	e.mu.Unlock()
	e.log.Info("心跳引擎已停止")
}

// Status 返回当前心跳状态
func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status
}

// Healthy 检查整体健康状态
func (e *Engine) Healthy() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.status.Running {
		return false
	}
	// 最近一次心跳在 2 倍间隔内即为健康
	return time.Since(e.status.LastSuccess) < time.Duration(e.cfg.IntervalSec*2)*time.Second
}

// loop 心跳主循环
func (e *Engine) loop() {
	ticker := time.NewTicker(time.Duration(e.cfg.IntervalSec) * time.Second)
	defer ticker.Stop()

	// 随机抖动 ±20% 避免所有实例同时心跳
	jitter := time.Duration(float64(e.cfg.IntervalSec)*0.2*rand.Float64()) * time.Second
	time.Sleep(jitter)

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.beat()
		}
	}
}

// beat 执行一次心跳
func (e *Engine) beat() {
	e.mu.Lock()
	e.status.Heartbeats++
	e.mu.Unlock()

	e.log.Debug("心跳 #%d 开始", e.status.Heartbeats)

	// 1. 检查 SSO 节点
	e.checkSSO()

	// 2. 检查所有业务站点
	e.checkSites()

	// 3. 刷新过期会话
	expired := e.pool.ExpireOld()
	if expired > 0 {
		e.log.Warn("%d 个会话已过期，将在下次心跳自动重建", expired)
	}

	e.mu.Lock()
	e.status.LastSuccess = time.Now()
	e.mu.Unlock()

	stats := e.pool.Stats()
	e.log.Debug("心跳 #%d 完成: active=%d/%d tokens=%d",
		e.status.Heartbeats, stats[StateActiveStr()], stats["total"], stats["token_active"])
}

// checkSSO 检查 SSO 节点健康
func (e *Engine) checkSSO() {
	eps := e.ssoClient.GetEndpoints()
	for i := range eps {
		ok, lat := e.ssoClient.Ping(eps[i].URL)
		if ok {
			e.log.Debug("SSO [%s] ✓ (%dms)", eps[i].Name, lat.Milliseconds())
		} else {
			e.log.Warn("SSO [%s] ✗ 不可达", eps[i].Name)
		}
	}
}

// checkSites 检查业务站点连通性
func (e *Engine) checkSites() {
	for i := range e.sites {
		site := &e.sites[i]
		url := site.URLs[0] // 主节点

		resp, err := e.client.Head(url)
		status := "ok"
		if err != nil {
			status = fmt.Sprintf("fail: %v", err)
			// 尝试备用节点
			if len(site.URLs) > 1 {
				for _, alt := range site.URLs[1:] {
					if r2, e2 := e.client.Head(alt); e2 == nil {
						r2.Body.Close()
						status = fmt.Sprintf("ok (via alt: %s)", alt)
						break
					}
				}
			}
		} else {
			resp.Body.Close()
		}

		e.mu.Lock()
		e.status.SiteHealth[site.Name] = status
		e.mu.Unlock()
	}
}

// StateActiveStr 返回 "活跃" 字符串
func StateActiveStr() string {
	return sso.StateActive.String()
}
