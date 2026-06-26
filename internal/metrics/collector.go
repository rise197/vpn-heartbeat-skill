// Package metrics — 运行时指标采集与上报
// 采集 SSO 认证耗时、站点访问成功率、心跳延迟等关键指标，
// 支持定时输出 JSON 格式的指标快照供外部监控系统消费。
package metrics

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
)

// Counter 单调递增计数器
type Counter struct {
	name  string
	value int64
	mu    sync.Mutex
}

func (c *Counter) Inc() {
	c.mu.Lock()
	c.value++
	c.mu.Unlock()
}

func (c *Counter) Add(n int64) {
	c.mu.Lock()
	c.value += n
	c.mu.Unlock()
}

func (c *Counter) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// Histogram 延迟分布统计
type Histogram struct {
	name    string
	count   int64
	sum     int64 // 微秒
	min     int64
	max     int64
	mu      sync.Mutex
}

func (h *Histogram) Observe(d time.Duration) {
	us := d.Microseconds()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sum += us
	if h.min == 0 || us < h.min {
		h.min = us
	}
	if us > h.max {
		h.max = us
	}
}

func (h *Histogram) Snapshot() (count int64, avgUs, minUs, maxUs int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count == 0 {
		return 0, 0, 0, 0
	}
	return h.count, h.sum / h.count, h.min, h.max
}

// Gauge 瞬时值指标
type Gauge struct {
	name  string
	value int64
	mu    sync.RWMutex
}

func (g *Gauge) Set(v int64) {
	g.mu.Lock()
	g.value = v
	g.mu.Unlock()
}

func (g *Gauge) Value() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.value
}

// Collector 指标采集器
// 聚合所有运行时指标，提供 JSON 快照输出
type Collector struct {
	ssoAuthTotal    *Counter
	ssoAuthSuccess  *Counter
	ssoAuthFail     *Counter
	ssoAuthLatency  *Histogram
	siteAccessTotal *Counter
	siteAccessOK    *Counter
	siteAccessFail  *Counter
	siteAccessLat   *Histogram
	heartbeatCount  *Counter
	activeSessions  *Gauge
	circuitOpens    *Counter

	log *logger.Logger
	mu  sync.RWMutex
}

// NewCollector 创建指标采集器实例
func NewCollector() *Collector {
	return &Collector{
		ssoAuthTotal:    &Counter{name: "sso_auth_total"},
		ssoAuthSuccess:  &Counter{name: "sso_auth_success"},
		ssoAuthFail:     &Counter{name: "sso_auth_fail"},
		ssoAuthLatency:  &Histogram{name: "sso_auth_latency_us"},
		siteAccessTotal: &Counter{name: "site_access_total"},
		siteAccessOK:    &Counter{name: "site_access_ok"},
		siteAccessFail:  &Counter{name: "site_access_fail"},
		siteAccessLat:   &Histogram{name: "site_access_latency_us"},
		heartbeatCount:  &Counter{name: "heartbeat_count"},
		activeSessions:  &Gauge{name: "active_sessions"},
		circuitOpens:    &Counter{name: "circuit_opens"},
		log:             logger.New("metrics"),
	}
}

// RecordSSOAuth 记录一次 SSO 认证
func (m *Collector) RecordSSOAuth(success bool, latency time.Duration) {
	m.ssoAuthTotal.Inc()
	if success {
		m.ssoAuthSuccess.Inc()
	} else {
		m.ssoAuthFail.Inc()
	}
	m.ssoAuthLatency.Observe(latency)
}

// RecordSiteAccess 记录一次业务站点访问
func (m *Collector) RecordSiteAccess(success bool, latency time.Duration) {
	m.siteAccessTotal.Inc()
	if success {
		m.siteAccessOK.Inc()
	} else {
		m.siteAccessFail.Inc()
	}
	m.siteAccessLat.Observe(latency)
}

// RecordHeartbeat 记录一次心跳
func (m *Collector) RecordHeartbeat() {
	m.heartbeatCount.Inc()
}

// SetActiveSessions 设置当前活跃会话数
func (m *Collector) SetActiveSessions(n int) {
	m.activeSessions.Set(int64(n))
}

// RecordCircuitOpen 记录一次熔断器打开事件
func (m *Collector) RecordCircuitOpen() {
	m.circuitOpens.Inc()
}

// Snapshot 返回当前所有指标的 JSON 格式化快照
// 可直接输出给外部监控系统（Prometheus, Grafana 等）
func (m *Collector) Snapshot() map[string]interface{} {
	ssoCount, ssoAvg, ssoMin, ssoMax := m.ssoAuthLatency.Snapshot()
	siteCount, siteAvg, siteMin, siteMax := m.siteAccessLat.Snapshot()

	ssoSuccessRate := 0.0
	if m.ssoAuthTotal.Value() > 0 {
		ssoSuccessRate = float64(m.ssoAuthSuccess.Value()) / float64(m.ssoAuthTotal.Value()) * 100
	}

	siteSuccessRate := 0.0
	if m.siteAccessTotal.Value() > 0 {
		siteSuccessRate = float64(m.siteAccessOK.Value()) / float64(m.siteAccessTotal.Value()) * 100
	}

	return map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339),
		"sso": map[string]interface{}{
			"total":        m.ssoAuthTotal.Value(),
			"success":      m.ssoAuthSuccess.Value(),
			"fail":         m.ssoAuthFail.Value(),
			"success_rate": fmt.Sprintf("%.1f%%", ssoSuccessRate),
			"latency_us": map[string]int64{
				"count": ssoCount,
				"avg":   ssoAvg,
				"min":   ssoMin,
				"max":   ssoMax,
			},
		},
		"sites": map[string]interface{}{
			"total":        m.siteAccessTotal.Value(),
			"ok":           m.siteAccessOK.Value(),
			"fail":         m.siteAccessFail.Value(),
			"success_rate": fmt.Sprintf("%.1f%%", siteSuccessRate),
			"latency_us": map[string]int64{
				"count": siteCount,
				"avg":   siteAvg,
				"min":   siteMin,
				"max":   siteMax,
			},
		},
		"heartbeats":    m.heartbeatCount.Value(),
		"active_sessions": m.activeSessions.Value(),
		"circuit_opens": m.circuitOpens.Value(),
	}
}

// SnapshotJSON 返回 JSON 字节
func (m *Collector) SnapshotJSON() []byte {
	data, _ := json.MarshalIndent(m.Snapshot(), "", "  ")
	return data
}
