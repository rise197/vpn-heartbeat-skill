// Package circuit — 熔断器模式实现
// 当 SSO 节点或业务站点连续失败达到阈值时自动熔断，
// 在半开状态下允许探测请求以检测恢复。
package circuit

import (
	"sync"
	"time"

	"github.com/rise197/vpn-heartbeat-skill/internal/logger"
)

// State 熔断器状态
type State int

const (
	StateClosed   State = iota // 正常通行
	StateOpen                   // 熔断打开，拒绝请求
	StateHalfOpen               // 半开探测，允许少量请求
)

func (s State) String() string {
	switch s {
	case StateClosed:   return "CLOSED"
	case StateOpen:     return "OPEN"
	case StateHalfOpen: return "HALF_OPEN"
	default:            return "UNKNOWN"
	}
}

// Config 熔断器配置
type Config struct {
	MaxFailures      int           // 连续失败阈值，触发熔断
	ResetTimeout     time.Duration // OPEN → HALF_OPEN 的冷却时间
	HalfOpenMaxReqs  int           // 半开状态下允许通过的探测请求数
	SuccessThreshold int           // 半开状态下连续成功几次后恢复 CLOSED
}

// DefaultConfig 返回生产环境推荐的熔断器配置
func DefaultConfig() Config {
	return Config{
		MaxFailures:      5,
		ResetTimeout:     30 * time.Second,
		HalfOpenMaxReqs:  3,
		SuccessThreshold: 2,
	}
}

// Breaker 熔断器实例
// 每个外部依赖（SSO 节点、业务站点）应独立持有自己的 Breaker
type Breaker struct {
	cfg Config

	mu              sync.Mutex
	state           State
	failures        int
	lastFailureTime time.Time
	halfOpenReqs    int
	halfOpenSuccess int
	name            string
	log             *logger.Logger
}

// New 创建一个新的熔断器实例
func New(name string, cfg Config) *Breaker {
	return &Breaker{
		cfg:   cfg,
		state: StateClosed,
		name:  name,
		log:   logger.New("circuit:" + name),
	}
}

// Call 在熔断器保护下执行操作 f
// 返回 f 的结果和熔断器当前状态
func (b *Breaker) Call(f func() error) (State, error) {
	b.mu.Lock()

	// 状态机转换检查
	switch b.state {
	case StateOpen:
		if time.Since(b.lastFailureTime) > b.cfg.ResetTimeout {
			b.log.Info("熔断器 [%s] OPEN → HALF_OPEN (冷却 %v 已到)", b.name, b.cfg.ResetTimeout)
			b.state = StateHalfOpen
			b.halfOpenReqs = 0
			b.halfOpenSuccess = 0
		} else {
			b.mu.Unlock()
			return StateOpen, ErrCircuitOpen
		}

	case StateHalfOpen:
		if b.halfOpenReqs >= b.cfg.HalfOpenMaxReqs {
			b.mu.Unlock()
			return StateHalfOpen, ErrCircuitHalfOpenLimited
		}
		b.halfOpenReqs++

	case StateClosed:
		// 正常通行，不做限制
	}

	b.mu.Unlock()

	// 执行实际操作
	err := f()

	b.mu.Lock()
	defer b.mu.Unlock()

	if err != nil {
		b.onFailure()
		return b.state, err
	}

	b.onSuccess()
	return b.state, nil
}

// State 返回当前熔断器状态
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Reset 手动重置熔断器到 CLOSED 状态
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = StateClosed
	b.failures = 0
	b.halfOpenReqs = 0
	b.halfOpenSuccess = 0
	b.log.Info("熔断器 [%s] 手动重置 → CLOSED", b.name)
}

func (b *Breaker) onSuccess() {
	b.failures = 0

	if b.state == StateHalfOpen {
		b.halfOpenSuccess++
		if b.halfOpenSuccess >= b.cfg.SuccessThreshold {
			b.log.Info("熔断器 [%s] HALF_OPEN → CLOSED (%d 次成功探测)", b.name, b.halfOpenSuccess)
			b.state = StateClosed
			b.halfOpenReqs = 0
			b.halfOpenSuccess = 0
		}
	}
}

func (b *Breaker) onFailure() {
	b.failures++
	b.lastFailureTime = time.Now()

	switch b.state {
	case StateClosed:
		if b.failures >= b.cfg.MaxFailures {
			b.log.Warn("熔断器 [%s] CLOSED → OPEN (连续 %d 次失败)", b.name, b.failures)
			b.state = StateOpen
		}
	case StateHalfOpen:
		b.log.Warn("熔断器 [%s] HALF_OPEN → OPEN (探测请求失败)", b.name)
		b.state = StateOpen
		b.halfOpenReqs = 0
		b.halfOpenSuccess = 0
	}
}

// 预定义错误
var (
	ErrCircuitOpen            = ErrCircuitOpen            = &CircuitError{msg: "熔断器已打开"}CircuitError{msg: "熔断器已打开，当前拒绝所有请求"}
	ErrCircuitHalfOpenLimited = &CircuitError{msg: "半开状态探测请求已达上限"}
)

// CircuitError 熔断器相关错误
type CircuitError struct {
	msg string
}

func (e *CircuitError) Error() string { return e.msg }
