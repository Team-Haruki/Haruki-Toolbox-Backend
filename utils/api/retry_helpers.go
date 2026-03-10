package api

import (
	"context"
	"time"
)

func RetryOperation(ctx context.Context, attempts int, interval time.Duration, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i == attempts-1 {
			break
		}
		if interval <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return lastErr
}
