package xbackoff

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v5"
)

type options struct {
	max        time.Duration
	multiplier float64
	jitter     float64
	sleeper    Sleeper
	onRetry    func(attempt int, delay time.Duration, err error)
	onReset    func()
}

func newOptions() *options {
	return &options{
		max:        backoff.DefaultMaxInterval,
		multiplier: backoff.DefaultMultiplier,
		jitter:     backoff.DefaultRandomizationFactor,
		sleeper:    TimerSleeper{},
	}
}

// Option configures a Backoff at construction time.
type Option func(*options)

// WithMax overrides the maximum interval between retries.
func WithMax(d time.Duration) Option {
	return func(o *options) { o.max = d }
}

// WithMultiplier overrides the exponential growth multiplier.
func WithMultiplier(m float64) Option {
	return func(o *options) { o.multiplier = m }
}

// WithRandomization overrides the jitter factor applied to each
// interval.
func WithRandomization(f float64) Option {
	return func(o *options) { o.jitter = f }
}

// WithSleeper installs a custom wait strategy.
func WithSleeper(s Sleeper) Option {
	return func(o *options) { o.sleeper = s }
}

// WithOnRetry installs a callback invoked AFTER op returned an error
// and BEFORE the Sleeper is engaged.
//
// Attempt starts at 1 for the first failure.
func WithOnRetry(f func(attempt int, delay time.Duration, err error)) Option {
	return func(o *options) { o.onRetry = f }
}

// WithOnReset installs a callback invoked from RunContext on success
// (after at least one retry happened) and from Reset when the
// internal counter is non-zero.
//
// The callback is only fired when the reset is meaningful, so it is safe to
// use as a "backoff cleared" signal.
func WithOnReset(f func()) Option {
	return func(o *options) { o.onReset = f }
}

// Backoff is a retry helper with exponential delay and a pluggable
// wait strategy.
type Backoff struct {
	initial    time.Duration
	max        time.Duration
	multiplier float64
	jitter     float64
	sleeper    Sleeper
	onRetry    func(attempt int, delay time.Duration, err error)
	onReset    func()

	inner   backoff.ExponentialBackOff
	attempt int
}

// New constructs a Backoff with the given initial interval.
func New(initial time.Duration, options ...Option) *Backoff {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &Backoff{
		initial:    initial,
		max:        opts.max,
		multiplier: opts.multiplier,
		jitter:     opts.jitter,
		sleeper:    opts.sleeper,
		onRetry:    opts.onRetry,
		onReset:    opts.onReset,
		inner: backoff.ExponentialBackOff{
			InitialInterval:     initial,
			RandomizationFactor: opts.jitter,
			Multiplier:          opts.multiplier,
			MaxInterval:         opts.max,
		},
	}
	m.inner.Reset()
	return m
}

// Reset returns the backoff to its initial interval and fires the
// onReset callback if at least one Next had been issued since the
// last reset.
func (m *Backoff) Reset() {
	had := m.attempt > 0
	m.inner.Reset()
	m.attempt = 0
	if had && m.onReset != nil {
		m.onReset()
	}
}

// Next returns the next backoff interval and advances the internal
// state.
//
// Successive calls grow geometrically, capped by the configured maximum.
func (m *Backoff) Next() time.Duration {
	m.attempt++
	return m.inner.NextBackOff()
}

// RunContext runs op until it succeeds, ctx is cancelled, or the
// Sleeper returns an error.
func (m *Backoff) RunContext(ctx context.Context, op func() error) error {
	m.Reset()
	for {
		err := op()
		if err == nil {
			if m.attempt > 0 && m.onReset != nil {
				m.onReset()
			}
			m.attempt = 0
			m.inner.Reset()
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}

		m.attempt++
		d := m.inner.NextBackOff()
		if m.onRetry != nil {
			m.onRetry(m.attempt, d, err)
		}
		if err := m.sleeper.Sleep(ctx, d); err != nil {
			return err
		}
	}
}
