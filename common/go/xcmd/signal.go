package xcmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

type Interrupted struct {
	os.Signal
}

func (m Interrupted) Error() string {
	return m.String()
}

func (m Interrupted) Is(target error) bool {
	_, ok := target.(Interrupted)
	return ok
}

// WaitInterrupted blocks until either SIGINT or SIGTERM signal is received or
// the provided context is canceled.
//
// The signals are captured only while it blocks: once it returns, a later
// SIGINT or SIGTERM is handled as if this call had never been made.
func WaitInterrupted(ctx context.Context) error {
	ch := make(chan os.Signal, 1)

	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(ch)
	select {
	case v := <-ch:
		return Interrupted{Signal: v}
	case <-ctx.Done():
		return ctx.Err()
	}
}
