package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	cfg := Config{
		MaxAttempts:   3,
		InitialDelay:  10 * time.Millisecond,
		BackoffFactor: 2.0,
	}

	callCount := 0
	err := Do(context.Background(), cfg, func(attempt int) error {
		callCount++
		return nil
	})

	if err != nil {
		t.Fatalf("期望成功，但得到错误: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("期望调用 1 次，实际 %d 次", callCount)
	}
}

func TestDo_RetryOnFailure(t *testing.T) {
	cfg := Config{
		MaxAttempts:   3,
		InitialDelay:  5 * time.Millisecond,
		MaxDelay:      50 * time.Millisecond,
		BackoffFactor: 2.0,
		JitterFactor:  0,
	}

	callCount := 0
	finalErr := errors.New("always fail")

	err := Do(context.Background(), cfg, func(attempt int) error {
		callCount++
		return finalErr
	})

	if err != finalErr {
		t.Fatalf("期望得到 finalErr，实际: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("期望最大重试 3 次，实际 %d 次", callCount)
	}
}

func TestDo_SucceedAfterRetry(t *testing.T) {
	cfg := Config{
		MaxAttempts:   5,
		InitialDelay:  5 * time.Millisecond,
		BackoffFactor: 2.0,
		JitterFactor:  0,
	}

	callCount := 0
	err := Do(context.Background(), cfg, func(attempt int) error {
		callCount++
		if attempt < 2 {
			return errors.New("temporary error")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("期望最终成功，但得到错误: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("期望调用 3 次（2 次失败 + 1 次成功），实际 %d 次", callCount)
	}
}

func TestDo_ContextCancellation(t *testing.T) {
	cfg := Config{
		MaxAttempts:   10,
		InitialDelay:  100 * time.Millisecond,
		BackoffFactor: 2.0,
		JitterFactor:  0,
	}

	ctx, cancel := context.WithCancel(context.Background())

	// 第一次调用后取消 context
	err := Do(ctx, cfg, func(attempt int) error {
		if attempt == 1 {
			cancel()
		}
		return errors.New("fail")
	})

	if err == nil {
		t.Fatal("期望 context 取消错误，但得到 nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxAttempts != 3 {
		t.Errorf("期望 MaxAttempts=3, 实际=%d", cfg.MaxAttempts)
	}
	if cfg.BackoffFactor != 2.0 {
		t.Errorf("期望 BackoffFactor=2.0, 实际=%f", cfg.BackoffFactor)
	}
}

func TestWithTimeout(t *testing.T) {
	cfg := Config{
		MaxAttempts:   10,
		InitialDelay:  50 * time.Millisecond,
		BackoffFactor: 2.0,
		JitterFactor:  0,
	}

	start := time.Now()
	err := WithTimeout(context.Background(), cfg, 100*time.Millisecond, func(attempt int) error {
		return errors.New("keep failing")
	})

	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("期望超时错误，但得到 nil")
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("期望在约 100ms 超时，但实际耗时 %v", elapsed)
	}
}
