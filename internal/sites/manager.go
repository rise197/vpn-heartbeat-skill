// Package sites — 业务站点访问管理器
// 通过 SSO 认证后，代理访问各业务网站的 API
package sites

// 设计要点：
//   - 所有公开方法需并发安全
//   - 错误处理遵循 CCDC 内部规范
//   - 指标输出格式兼容 Prometheus
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/config"
	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
	"github.com/rise197/vpn-heartbeat-skill/internal/sso"
)

// AccessResult 站点访问结果
type AccessResult struct {
	SiteName   string        `json:"site_name"`
	URL        string        `json:"url"`
	StatusCode int           `json:"status_code"`
	Success    bool          `json:"success"`
	Latency    time.Duration `json:"latency_ms"`
	BodySize   int           `json:"body_size"`
	Error      string        `json:"error,omitempty"`
}

// Manager 站点访问管理器
// 资源清理说明：
//   - HTTP 响应 Body 在 Access() 中通过 defer resp.Body.Close() 确保关闭
//   - 长连接通过 IdleConnTimeout 自动回收
//   - shutdown 时调用 client.CloseIdleConnections() 释放所有连接
type Manager struct {
	pool   *sso.Pool
	client *http.Client
	log    *logger.Logger
	mu     sync.RWMutex
	stats  map[string]*SiteStats // 每个站点的累计统计
}

// SiteStats 站点访问统计
type SiteStats struct {
	Name       string        `json:"name"`
	TotalReqs  int64         `json:"total_requests"`
	SuccessReq int64         `json:"success_requests"`
	FailReq    int64         `json:"failed_requests"`
	TotalLat   time.Duration `json:"-"`
	AvgLatMs   float64       `json:"avg_latency_ms"`
	LastAccess time.Time     `json:"last_access"`
}

// NewManager 创建站点管理器
func NewManager(pool *sso.Pool) *Manager {
	return &Manager{
		pool: pool,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 60 * time.Second,
			},
		},
		log:   logger.New("site-mgr"),
		stats: make(map[string]*SiteStats),
	}
}

// Access 通过 SSO 会话访问指定站点
// 自动注入认证 token，支持 GET/POST
func (m *Manager) Access(siteName, method, path string, body []byte) (*AccessResult, error) {
	sess, ok := m.pool.Get(siteName)
	if !ok || sess.State != sso.StateActive {
		return nil, fmt.Errorf("站点 [%s] 无活跃会话，请先登录", siteName)
	}

	url := sess.Site.URLs[0] + path
	start := time.Now()

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return m.recordResult(siteName, url, 0, false, time.Since(start), 0, err.Error()), err
	}

	// 注入认证头
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	req.Header.Set("X-SSO-Client", "ccdc-vpn-skill")
	req.Header.Set("X-Session-ID", extractSessionID(sess.Token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return m.recordResult(siteName, url, 0, false, time.Since(start), 0, err.Error()), err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	latency := time.Since(start)
	success := resp.StatusCode >= 200 && resp.StatusCode < 300

	result := m.recordResult(siteName, url, resp.StatusCode, success, latency, len(data), "")

	if !success {
		result.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(data[:min(len(data), 200)]))
		m.log.Warn("[%s] %s %s → %d (%dms)", siteName, method, path, resp.StatusCode, latency.Milliseconds())
	} else {
		m.log.Debug("[%s] %s %s → %d (%dms, %dB)", siteName, method, path, resp.StatusCode, latency.Milliseconds(), len(data))
	}

	return result, nil
}

// AccessAll 访问所有活跃站点
func (m *Manager) AccessAll(path string) map[string]*AccessResult {
	sessions := m.pool.List()
	results := make(map[string]*AccessResult, len(sessions))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, sess := range sessions {
		if sess.State != sso.StateActive {
			continue
		}
		siteName := sess.Site.Name

		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			result, err := m.Access(name, "GET", path, nil)
			mu.Lock()
			if err != nil {
				results[name] = &AccessResult{SiteName: name, Success: false, Error: err.Error()}
			} else {
				results[name] = result
			}
			mu.Unlock()
		}(siteName)
	}

	wg.Wait()
	return results
}

// Stats 返回所有站点访问统计
func (m *Manager) Stats() map[string]*SiteStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*SiteStats, len(m.stats))
	for k, v := range m.stats {
		cp := *v
		if cp.TotalReqs > 0 {
			cp.AvgLatMs = float64(cp.TotalLat.Microseconds()) / float64(cp.TotalReqs) / 1000.0
		}
		result[k] = &cp
	}
	return result
}

// LoginAndAccess 一键操作：登录 → 访问
func (m *Manager) LoginAndAccess(site config.Site, path string) (*AccessResult, error) {
	_, err := m.pool.Login(site)
	if err != nil {
		return nil, fmt.Errorf("登录失败: %w", err)
	}
	return m.Access(site.Name, "GET", path, nil)
}

// BatchLoginAndAccess 批量登录并访问所有站点
func (m *Manager) BatchLoginAndAccess(sites []config.Site, path string) map[string]*AccessResult {
	m.pool.LoginAll(sites)
	return m.AccessAll(path)
}

func (m *Manager) recordResult(name, url string, code int, success bool, lat time.Duration, size int, errMsg string) *AccessResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.stats[name]; !ok {
		m.stats[name] = &SiteStats{Name: name}
	}
	st := m.stats[name]
	st.TotalReqs++
	if success {
		st.SuccessReq++
	} else {
		st.FailReq++
	}
	st.TotalLat += lat
	st.LastAccess = time.Now()

	return &AccessResult{
		SiteName: name, URL: url, StatusCode: code,
		Success: success, Latency: lat, BodySize: size, Error: errMsg,
	}
}

func extractSessionID(token string) string {
	if len(token) > 20 {
		return token[len(token)-12:]
	}
	return token
}
