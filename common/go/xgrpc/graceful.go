// Package xgrpc provides generic gRPC helpers shared across the gateway,
// operators, and modules.
package xgrpc

import (
	"time"

	"google.golang.org/grpc"
)

// GracefulStopTimeout bounds how long StopGracefully's callers wait for
// grpc.Server.GracefulStop to drain in-flight RPCs before forcing the
// shutdown.
//
// GracefulStop blocks until every active RPC completes, and a
// server-streaming call is active for as long as its client keeps it open —
// an unattended readiness watch, or one merely proxied for an operator,
// would otherwise wedge shutdown forever and turn a SIGTERM into a hung
// process. Cutting the grace window short trades a clean drain for a bounded
// exit: forcing the stop only closes connections, it never aborts a handler
// goroutine mid-execution, so a config write already in flight still runs to
// completion and only the client's connection is lost.
const GracefulStopTimeout = 5 * time.Second

// StopGracefully stops server, giving in-flight RPCs up to timeout to finish
// before forcing the shutdown with Stop.
//
// timeout bounds only the grace window before the stop is forced, not the
// total time this call takes. Once forced, StopGracefully still waits for
// every in-flight handler goroutine to return, deliberately: Stop only
// closes connections and cancels RPC contexts, it cannot abort a handler
// that is part-way through a shared-memory config write, and returning out
// from under one would let the process exit while that write is torn. So
// the effective wait is the longer of timeout and the slowest in-flight
// handler. The backstop against a handler that never returns is not this
// function's timeout but the service unit's own stop timeout, which is
// expected to SIGKILL the process if shutdown overruns it.
//
// onForceStop is called exactly when timeout elapses and the stop must be
// forced, immediately before server.Stop(). It is nil-safe: a caller that
// does not care about the event may pass nil.
func StopGracefully(server *grpc.Server, timeout time.Duration, onForceStop func()) {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		if onForceStop != nil {
			onForceStop()
		}
		server.Stop()
		// Deliberately unbounded: Stop() cancels RPC contexts but cannot
		// abort a handler mid-shared-memory-write, and exiting underneath
		// one would tear that write.
		<-done
	}
}
