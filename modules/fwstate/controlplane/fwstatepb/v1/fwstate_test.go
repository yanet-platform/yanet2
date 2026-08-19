package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

const cfwstateTTL48Max = cfwstate.TTL48Max

func TestValidateSyncConfigTimeouts(t *testing.T) {
	valid := &SyncConfig{
		TcpSynAck: 120e9,
		TcpSyn:    120e9,
		TcpFin:    120e9,
		Tcp:       120e9,
		Udp:       30e9,
		Default:   16e9,
	}
	require.NoError(t, valid.ValidateTimeouts())

	tooLarge := uint64(1) << 48
	invalid := &SyncConfig{
		TcpSynAck: 120e9,
		TcpSyn:    tooLarge,
		TcpFin:    120e9,
		Tcp:       120e9,
		Udp:       tooLarge,
		Default:   16e9,
	}
	err := invalid.ValidateTimeouts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "tcp_syn")
	require.Contains(t, err.Error(), "udp")
}

// TestValidateSyncConfigTimeoutsSuppressOverflow verifies that a suppress
// window large enough to push an otherwise-valid timeout past the 48-bit
// last_ttl limit is rejected, since the dataplane stores the inflated
// (timeout + suppress) value.
func TestValidateSyncConfigTimeoutsSuppressOverflow(t *testing.T) {
	// A suppress window that by itself fits, but added to the default timeout
	// overflows the 48-bit field.
	overflowing := &SyncConfig{
		Tcp:                 cfwstateTTL48Max,
		SyncSuppressTimeout: 1,
	}
	err := overflowing.ValidateTimeouts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "tcp+sync_suppress_timeout")

	// The same timeout with no suppress, or with a window that still fits, is
	// accepted.
	require.NoError(t, (&SyncConfig{Tcp: cfwstateTTL48Max}).ValidateTimeouts())
	require.NoError(t, (&SyncConfig{
		Tcp:                 120e9,
		SyncSuppressTimeout: 8e9,
	}).ValidateTimeouts())
}
