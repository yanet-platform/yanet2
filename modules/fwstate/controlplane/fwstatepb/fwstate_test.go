package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
