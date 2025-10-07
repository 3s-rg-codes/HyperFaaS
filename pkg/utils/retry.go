package utils

import (
	"context"
	"fmt"
	"time"
)

// CallWithRetry calls a function, retrying maxAttempts times if it returns an error.
// If after maxAttempts the function still returns an error, it returns the zero value of T and the error.
func CallWithRetry[T any](ctx context.Context, fn func() (T, error), maxAttempts int, backoff time.Duration) (T, error) {
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		t, err := fn()
		if err == nil {
			return t, nil
		}
		lastErr = err
		if i < maxAttempts-1 {
			time.Sleep(backoff)
		}
	}
	var zero T
	return zero, fmt.Errorf("failed after %d attempts: %w", maxAttempts, lastErr)
}
