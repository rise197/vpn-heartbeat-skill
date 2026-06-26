// Package retry — 指数退避重试策略
// 为 SSO 认证、站点访问等网络操作提供健壮的失败重试机制，
// 支持最大重试次数、初始延迟、退避因子和抖动配置。
package retry

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
)

// Config 重试策略配置参数
type Config struct {
	MaxAttempts   int           // 最大重试次数（含首次），0 表示不限
	InitialDelay  time.Duration // 首次重试前等待时间
	MaxDelay      time.Duration // 单次重试最大等待时间上限
	BackoffFactor float64       // 退避乘法因子，通常 2.0
	JitterFactor  float64       // 抖动系数 [0, 1]，0 表示无抖动
}

// DefaultConfig 返回生产环境推荐的重试配置
func DefaultConfig() Config {
	return Config{
		MaxAttempts:   3,
		InitialDelay:  500 * time.Millisecond,
		MaxDelay:      15 * time.Second,
		BackoffFactor: 2.0,
		JitterFactor:  0.2,
	}
}

// Do 使用指定的重试策略执行操作 f。
// f 返回 error 表示需要重试。
// 重试之间按指数退避 + 随机抖动计算等待时间。
//
// 重试间隔计算公式:
//
//	delay = min(initialDelay * backoffFactor^attempt, maxDelay)
//	jittered = delay * (1 + jitterFactor * (2*random - 1))
//
// 其中 attempt 从 0 开始计数。
func Do(ctx context.Context, cfg Config, f func(attempt int) error) error {
	log := logger.New("retry")

	var lastErr error
	for attempt := 0; cfg.MaxAttempts == 0 || attempt < cfg.MaxAttempts; attempt++ {
		// 执行操作
		err := f(attempt)
		if err == nil {
			if attempt > 0 {
				log.Debug("操作在第 %d 次尝试后成功", attempt+1)
			}
			return nil
		}

		lastErr = err

		// 最后一次尝试不等待
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		// 计算退避延迟
		delay := time.Duration(float64(cfg.InitialDelay) * math.Pow(cfg.BackoffFactor, float64(attempt)))
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}

		// 施加随机抖动以分散重试风暴
		if cfg.JitterFactor > 0 {
			jitterDelta := time.Duration(float64(delay) * cfg.JitterFactor * (2*rand.Float64() - 1))
			delay += jitterDelta
		}

		if delay < 0 {
			delay = cfg.InitialDelay
		}

		log.Warn("第 %d 次尝试失败: %v, %v 后重试", attempt+1, err, delay.Round(time.Millisecond))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	return lastErr
}

// WithTimeout 包装 Do 并增加整体超时控制
func WithTimeout(ctx context.Context, cfg Config, timeout time.Duration, f func(attempt int) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return Do(ctx, cfg, f)
}
