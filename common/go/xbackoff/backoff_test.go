package xbackoff_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yanet-platform/yanet2/common/go/xbackoff"
)

// Test_RunContext_OperationCancellation verifies that canceling from an
// operation preserves both the cancellation and its joined failure cause.
func Test_RunContext_OperationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	operationErr := errors.New("rollback failed")
	retry := xbackoff.New(time.Hour)

	err := retry.RunContext(ctx, func() error {
		cancel()
		return errors.Join(context.Canceled, operationErr)
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if !errors.Is(err, operationErr) {
		t.Fatalf("expected operation error, got %v", err)
	}
}

// Test_RunContext_SleepCancellation verifies that canceling the retry delay
// preserves the preceding operation failure alongside the cancellation.
func Test_RunContext_SleepCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	operationErr := errors.New("rollback failed")
	retry := xbackoff.New(time.Hour, xbackoff.WithSleeper(sleeperFunc(func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	})))

	err := retry.RunContext(ctx, func() error { return operationErr })

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if !errors.Is(err, operationErr) {
		t.Fatalf("expected operation error, got %v", err)
	}
}

type sleeperFunc func(context.Context, time.Duration) error

func (m sleeperFunc) Sleep(ctx context.Context, duration time.Duration) error {
	return m(ctx, duration)
}
