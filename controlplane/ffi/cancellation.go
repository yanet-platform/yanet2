package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/cancellation -lcancellation
//
//#include "lib/controlplane/cancellation/cancellation.h"
import "C"

import (
	"context"
	"runtime"
)

// WithCancellation runs a blocking C call under the context's control.
//
// A one-shot token is bound to the calling OS thread for the duration of
// the call and raised when the context ends, so C code can poll for the
// request without a token threaded through every signature. A context
// that cannot be cancelled binds nothing instead. The call is never
// abandoned: this returns only after the call itself has returned, and a
// context that is already done fails fast without entering C.
func WithCancellation(ctx context.Context, call func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// The token is observed through thread-local state, so the goroutine
	// must not migrate between binding and the call.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// A call that cannot be cancelled still replaces the binding, so it
	// does not inherit the request that some enclosing call is running
	// under and give up on work that one was allowed to abandon.
	if ctx.Done() == nil {
		previous := C.cancellation_token_bind(nil)
		defer C.cancellation_token_bind(previous)

		return call()
	}

	token := new(C.struct_cancellation_token)

	var pinner runtime.Pinner
	pinner.Pin(token)
	defer pinner.Unpin()

	previous := C.cancellation_token_bind(token)
	defer C.cancellation_token_bind(previous)

	stop := context.AfterFunc(ctx, func() {
		C.cancellation_token_fire(token)
	})
	defer stop()

	return call()
}
