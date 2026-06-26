// Package main — VPN Heartbeat Skill 入口
//
// CCDC 实习第一个业务系统 SKILL 开发作品
// 功能:
//   - SSO 单点登录（多节点故障转移）
//   - VPN 心跳保持（60s 间隔）
//   - 多业务站点自动认证访问
//   - 会话池管理 + 令牌签发
//   - 站点健康监控 + 统计
//   - 优雅关闭
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/auth"
	"github.com/rise197/vpn-heartbeat-skill/internal/config"
	"github.com/rise197/vpn-heartbeat-skill/internal/heartbeat"
	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
	"github.com/rise197/vpn-heartbeat-skill/internal/sites"
	"github.com/rise197/vpn-heartbeat-skill/internal/sso"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	once := flag.Bool("once", false, "单次模式: SSO 认证后输出 JSON 并退出（AI 首次加载使用）")
	flag.Parse()

	logger.Info("========================================")
	logger.Info("  CCDC VPN Heartbeat Skill v1.0")
	logger.Info("  AI 助手自动化 SSO 多站点访问引擎")
	logger.Info("========================================")

	// 1. 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("配置加载失败: %v", err)
		os.Exit(1)
	}
	logger.Info("业务站点: %d 个 | 心跳间隔: %ds | SSO 节点: %d 个",
		len(cfg.Sites), cfg.Heartbeat.IntervalSec, len(cfg.SSO.URLs))

	// 2. 初始化 SSO 客户端（带故障转移）
	ssoEndpoints := make([]sso.Endpoint, len(cfg.SSO.URLs))
	for i, url := range cfg.SSO.URLs {
		ssoEndpoints[i] = sso.Endpoint{
			Name:     fmt.Sprintf("SSO-%d", i+1),
			URL:      url,
			Priority: i + 1,
		}
	}
	ssoClient := sso.NewClient(ssoEndpoints)
	logger.Info("SSO 客户端就绪: %d 个节点", len(ssoEndpoints))

	// 3. 初始化令牌管理器
	tm := auth.NewTokenManager("ccdc-vpn-skill-secret-key-2026", 8*time.Hour)

	// 4. 创建会话池
	pool := sso.NewPool(ssoClient, tm)

	// 5. 站点访问管理器
	siteMgr := sites.NewManager(pool)

	// 6. 心跳引擎
	engine := heartbeat.New(cfg.Heartbeat, pool, ssoClient, cfg.Sites)

	// 7. SSO 认证 → 批量登录所有站点
	logger.Info("正在通过 SSO 登录所有业务站点...")
	results := pool.LoginAll(cfg.Sites)

	activeCount := 0
	for name, sess := range results {
		if sess.State == sso.StateActive {
			activeCount++
			logger.Info("  ✓ %s — 已认证", name)
		} else {
			logger.Error("  ✗ %s — 认证失败", name)
		}
	}
	logger.Info("认证完成: %d/%d 活跃", activeCount, len(cfg.Sites))

	// 8. 对所有活跃站点执行一次探测访问
	logger.Info("正在探测各站点连通性...")
	accessResults := siteMgr.AccessAll("/")
	for name, r := range accessResults {
		if r.Success {
			logger.Info("  ✓ %s — %d (%dms)", name, r.StatusCode, r.Latency.Milliseconds())
		} else {
			logger.Warn("  ✗ %s — %s", name, r.Error)
		}
	}

	// 9. --once 模式：输出 JSON 结果后退出
	if *once {
		outputOnceResult(activeCount, len(cfg.Sites), accessResults)
		return
	}

	// 10. 启动心跳引擎
	engine.Start()
	logger.Info("心跳引擎已启动")

	// 10. 打印运行状态
	printStatus(engine, pool, siteMgr)

	// 11. 等待退出信号（长驻模式）
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 定时打印状态
	statusTicker := time.NewTicker(5 * time.Minute)
	defer statusTicker.Stop()

	for {
		select {
		case sig := <-sigCh:
			logger.Info("收到信号 %v，正在优雅关闭...", sig)
			engine.Stop()
			logger.Info("已关闭，再见")
			return

		case <-statusTicker.C:
			printStatus(engine, pool, siteMgr)
		}
	}
}

// outputOnceResult 单次模式 — 输出 JSON 给 AI 解析
func outputOnceResult(active, total int, accessResults map[string]*sites.AccessResult) {
	type SiteResult struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Code    int    `json:"http_code"`
		Latency int64  `json:"latency_ms"`
		Error   string `json:"error,omitempty"`
	}

	type OnceOutput struct {
		Success      bool         `json:"success"`
		Message      string       `json:"message"`
		AuthTotal    int          `json:"auth_total"`
		AuthActive   int          `json:"auth_active"`
		Sites        []SiteResult `json:"sites"`
		SSOConnected bool         `json:"sso_connected"`
	}

	output := OnceOutput{
		Success:      active > 0,
		AuthTotal:    total,
		AuthActive:   active,
		SSOConnected: active > 0,
	}

	if active == total {
		output.Message = fmt.Sprintf("全部 %d 个站点 SSO 认证成功", active)
	} else if active > 0 {
		output.Message = fmt.Sprintf("部分成功: %d/%d 个站点认证通过", active, total)
	} else {
		output.Message = "SSO 认证全部失败，请检查单点登录系统连通性"
		output.SSOConnected = false
	}

	for _, r := range accessResults {
		sr := SiteResult{Name: r.SiteName, Status: "fail", Error: r.Error}
		if r.Success {
			sr.Status = "ok"
			sr.Code = r.StatusCode
			sr.Latency = r.Latency.Milliseconds()
		}
		output.Sites = append(output.Sites, sr)
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
}

func printStatus(engine *heartbeat.Engine, pool *sso.Pool, siteMgr *sites.Manager) {
	status := engine.Status()
	poolStats := pool.Stats()

	logger.Info("========== 运行状态 ==========")
	logger.Info("心跳次数: %d | 失败: %d | 最后成功: %s",
		status.Heartbeats, status.Failures,
		status.LastSuccess.Format("15:04:05"))

	logger.Info("会话池: %d 活跃 / %d 总数 | 令牌: %d",
		poolStats["活跃"], poolStats["total"], poolStats["token_active"])

	logger.Info("站点健康:")
	for name, health := range status.SiteHealth {
		logger.Info("  %s: %s", name, health)
	}

	siteStats := siteMgr.Stats()
	logger.Info("站点访问统计:")
	for name, st := range siteStats {
		logger.Info("  %s: %d次请求 %.1fms平均 | 成功率 %.1f%%",
			name, st.TotalReqs, st.AvgLatMs,
			float64(st.SuccessReq)/float64(max(1, st.TotalReqs))*100)
	}
	logger.Info("==============================")
}
