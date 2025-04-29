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

// WaitInterrupted blocks until either SIGINT or SIGTERM signal is received or
// the provided context is canceled.
func WaitInterrupted(ctx context.Context) error {
	ch := make(chan os.Signal, 1)

	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	select {
	case v := <-ch:
		return Interrupted{Signal: v}
	case <-ctx.Done():
		return ctx.Err()
	}
}
