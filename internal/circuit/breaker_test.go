package circuit

import (
	"errors"
	"testing"
	"time"
)

func TestBreaker_ClosedToOpen(t *testing.T) {
	cfg := Config{
		MaxFailures:      3,
		ResetTimeout:     1 * time.Second,
		HalfOpenMaxReqs:  2,
		SuccessThreshold: 1,
	}

	b := New("test-closed-to-open", cfg)
	errTest := errors.New("simulated failure")

	// 前 2 次失败 — 仍为 CLOSED
	for i := 0; i < 2; i++ {
		state, err := b.Call(func() error { return errTest })
		if state != StateClosed {
			t.Fatalf("第 %d 次: 期望 CLOSED, 实际 %s", i+1, state)
		}
		if err != errTest {
			t.Fatalf("期望 errTest, 实际 %v", err)
		}
	}

	// 第 3 次失败触发熔断
	state, _ := b.Call(func() error { return errTest })
	if state != StateOpen {
		t.Fatalf("期望 OPEN, 实际 %s", state)
	}
}

func TestBreaker_OpenToHalfOpen(t *testing.T) {
	cfg := Config{
		MaxFailures:      1,
		ResetTimeout:     50 * time.Millisecond,
		HalfOpenMaxReqs:  2,
		SuccessThreshold: 1,
	}

	b := New("test-open-to-halfopen", cfg)

	// 触发熔断
	b.Call(func() error { return errors.New("fail") })
	if b.State() != StateOpen {
		t.Fatal("期望 OPEN 状态")
	}

	// 等待冷却
	time.Sleep(60 * time.Millisecond)

	// 半开状态 — 允许探测请求
	state, err := b.Call(func() error { return nil })
	if state != StateHalfOpen {
		t.Fatalf("期望 HALF_OPEN (探测), 实际 %s", state)
	}
	if err != nil {
		t.Fatalf("探测请求应成功: %v", err)
	}
}

func TestBreaker_HalfOpenToClosed(t *testing.T) {
	cfg := Config{
		MaxFailures:      1,
		ResetTimeout:     30 * time.Millisecond,
		HalfOpenMaxReqs:  5,
		SuccessThreshold: 2,
	}

	b := New("test-recovery", cfg)
	b.Call(func() error { return errors.New("fail") })
	time.Sleep(40 * time.Millisecond)

	// 连续成功
	b.Call(func() error { return nil })
	b.Call(func() error { return nil })

	if b.State() != StateClosed {
		t.Fatalf("期望恢复 CLOSED (2 次成功探测), 实际 %s", b.State())
	}
}

func TestBreaker_HalfOpenFailure(t *testing.T) {
	cfg := Config{
		MaxFailures:      1,
		ResetTimeout:     30 * time.Millisecond,
		HalfOpenMaxReqs:  3,
		SuccessThreshold: 2,
	}

	b := New("test-halfopen-fail", cfg)
	b.Call(func() error { return errors.New("fail") })
	time.Sleep(40 * time.Millisecond)

	// 半开状态下失败 → 重新 OPEN
	state, _ := b.Call(func() error { return errors.New("probe fail") })
	if state != StateOpen {
		t.Fatalf("探测失败后期望重新 OPEN, 实际 %s", state)
	}
}

func TestBreaker_ManualReset(t *testing.T) {
	cfg := DefaultConfig()
	b := New("test-reset", cfg)

	for i := 0; i < cfg.MaxFailures; i++ {
		b.Call(func() error { return errors.New("fail") })
	}
	if b.State() != StateOpen {
		t.Fatal("期望熔断打开")
	}

	b.Reset()
	if b.State() != StateClosed {
		t.Fatal("期望手动重置后恢复 CLOSED")
	}
}

func TestBreaker_SuccessResetsFailureCount(t *testing.T) {
	cfg := Config{MaxFailures: 3, ResetTimeout: 1 * time.Second}
	b := New("test-reset-count", cfg)

	// 2 次失败
	b.Call(func() error { return errors.New("f1") })
	b.Call(func() error { return errors.New("f2") })

	// 1 次成功 — 重置计数
	b.Call(func() error { return nil })

	if b.State() != StateClosed {
		t.Fatalf("成功后应保持 CLOSED, 实际 %s", b.State())
	}
}

func TestErrCircuitOpen(t *testing.T) {
	cfg := Config{MaxFailures: 1, ResetTimeout: 10 * time.Second}
	b := New("test-err", cfg)

	b.Call(func() error { return errors.New("fail") })
	_, err := b.Call(func() error { return nil })

	if err != ErrCircuitOpen {
		t.Fatalf("期望 ErrCircuitOpen, 实际 %v", err)
	}
}
