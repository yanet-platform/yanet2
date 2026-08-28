package ffi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Test_WithCancellation_DoneContextFailsFast verifies that a context that
// is already done reports its own error without entering the call.
func Test_WithCancellation_DoneContextFailsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	called := false
	err := ffi.WithCancellation(ctx, func() error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
}

// Test_WithCancellation_ReturnsCallError verifies that the call's own
// error passes through unchanged while the context stays live.
func Test_WithCancellation_ReturnsCallError(t *testing.T) {
	callErr := errors.New("call failed")
	err := ffi.WithCancellation(t.Context(), func() error {
		return callErr
	})
	require.ErrorIs(t, err, callErr)
}
