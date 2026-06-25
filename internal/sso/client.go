// Package sso — 单点登录客户端，多节点故障转移认证
package sso

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
)

// Endpoint SSO 节点
type Endpoint struct {
	Name     string
	URL      string
	Priority int
}

// Client SSO 认证客户端
type Client struct {
	endpoints []Endpoint
	client    *http.Client
	log       *logger.Logger
}

// LoginRequest SSO 登录请求
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Client   string `json:"client"`
}

// LoginResponse SSO 登录响应
type LoginResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// NewClient 创建 SSO 客户端
func NewClient(endpoints []Endpoint) *Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &Client{
		endpoints: sortEndpoints(endpoints),
		client:    &http.Client{Transport: tr, Timeout: 15 * time.Second},
		log:       logger.New("sso-client"),
	}
}

// Authenticate 按优先级尝试所有 SSO 节点进行登录验证
// 401/403 → 直接返回错误（凭据无效）
// 超时/不可达 → 自动切换下一节点
func (c *Client) Authenticate(username, password string) (*LoginResponse, *Endpoint, error) {
	for i := range c.endpoints {
		ep := &c.endpoints[i]
		c.log.Info("尝试认证: %s (%s)", ep.Name, ep.URL)

		resp, err := c.doLogin(ep.URL, username, password)
		if err != nil {
			c.log.Warn("%s 不可达: %v, 切换下一节点", ep.Name, err)
			continue
		}

		// 200 → 认证成功
		if resp.Code == 200 || resp.Code == 0 {
			c.log.Info("✓ 认证成功 → %s", ep.Name)
			return resp, ep, nil
		}

		// 401/403 → 凭据错误，不重试
		if resp.Code == 401 || resp.Code == 403 {
			c.log.Error("✗ %s 返回 %d: %s", ep.Name, resp.Code, resp.Message)
			return resp, ep, fmt.Errorf("sso: %s", resp.Message)
		}

		// 其他 HTTP 错误 → 尝试下一节点
		c.log.Warn("%s 返回 %d，切换下一节点", ep.Name, resp.Code)
	}

	return nil, nil, fmt.Errorf("sso: 全部 %d 个节点不可达", len(c.endpoints))
}

// Ping 探测 SSO 节点可达性
func (c *Client) Ping(url string) (bool, time.Duration) {
	start := time.Now()
	resp, err := c.client.Head(url)
	if err != nil {
		return false, time.Since(start)
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500, time.Since(start)
}

// HealthCheck 对所有 SSO 节点执行健康检查
func (c *Client) HealthCheck() map[string]bool {
	result := make(map[string]bool, len(c.endpoints))
	for i := range c.endpoints {
		ok, _ := c.Ping(c.endpoints[i].URL)
		result[c.endpoints[i].Name] = ok
	}
	return result
}

// GetEndpoints 返回节点列表（只读）
func (c *Client) GetEndpoints() []Endpoint {
	return append([]Endpoint{}, c.endpoints...)
}

// doLogin 向指定 URL 发送登录请求
func (c *Client) doLogin(url string, username, password string) (*LoginResponse, error) {
	body := LoginRequest{
		Username: username,
		Password: password,
		Client:   "ccdc-vpn-skill",
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	loginURL := url + "/api/auth/login"
	req, err := http.NewRequest("POST", loginURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SSO-Client", "ccdc-vpn-skill")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var loginResp LoginResponse
	if err := json.Unmarshal(data, &loginResp); err != nil {
		// 如果不能解析，以 HTTP 状态码为准
		return &LoginResponse{Code: resp.StatusCode, Message: string(data)}, nil
	}

	return &loginResp, nil
}

// sortEndpoints 按优先级排序节点
func sortEndpoints(eps []Endpoint) []Endpoint {
	sort.Slice(eps, func(i, j int) bool {
		return eps[i].Priority < eps[j].Priority
	})
	return eps
}
