package xbackoff

import (
	"context"
	"errors"
	"time"
)

// ErrInterrupted is the canonical sentinel a Sleeper returns to ask
// RunContext to stop without invoking op once more.
var ErrInterrupted = errors.New("xbackoff: sleep interrupted")

// Sleeper is the strategy for waiting between retry attempts.
//
// Sleep blocks for d.
//
// It MUST honour ctx cancellation and return ctx.Err() in that case.
//
// It MAY return ErrInterrupted (or any other error) to signal that RunContext
// should bail out without attempting op again.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// TimerSleeper is the default Sleeper.
type TimerSleeper struct{}

// Sleep blocks for d or until ctx is cancelled.
func (m TimerSleeper) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
