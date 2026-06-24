// Package config — YAML 配置加载，定义所有业务站点与 SSO 端点
package config

import (
	"os"

	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
	"gopkg.in/yaml.v3"
)

// Site 业务网站配置
type Site struct {
	Name     string   `yaml:"name"`     // 系统名称
	URLs     []string `yaml:"urls"`     // 多节点地址（优先第一个）
	Username string   `yaml:"username"` // 登录账号
	Password string   `yaml:"password"` // 登录密码
	Category string   `yaml:"category"` // 分类标签
}

// HeartbeatConfig VPN 心跳参数
type HeartbeatConfig struct {
	IntervalSec int `yaml:"interval_sec"` // 心跳间隔（秒）
	TimeoutSec  int `yaml:"timeout_sec"`  // 单次超时（秒）
	RetryMax    int `yaml:"retry_max"`    // 最大重试次数
}

// AppConfig 应用总配置
type AppConfig struct {
	SSO       SSOConfig       `yaml:"sso"`
	Heartbeat HeartbeatConfig `yaml:"heartbeat"`
	Sites     []Site          `yaml:"sites"`
}

// SSOConfig 单点登录配置
type SSOConfig struct {
	Name     string   `yaml:"name"`
	URLs     []string `yaml:"urls"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
}

// Load 从 YAML 文件加载配置
func Load(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &AppConfig{
		Heartbeat: HeartbeatConfig{
			IntervalSec: 60,
			TimeoutSec:  10,
			RetryMax:    3,
		},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	logger.Info("配置加载完成: %d 个业务站点", len(cfg.Sites))
	return cfg, nil
}

// Save 将默认配置写入 YAML 文件
func Save(path string, cfg *AppConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
