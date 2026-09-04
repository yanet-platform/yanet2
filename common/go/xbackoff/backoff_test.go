package xbackoff_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yanet-platform/yanet2/common/go/xbackoff"
)

func TestRunContextPreservesOperationErrorOnCancellation(t *testing.T) {
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

func TestRunContextPreservesOperationErrorWhenSleepIsCanceled(t *testing.T) {
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
