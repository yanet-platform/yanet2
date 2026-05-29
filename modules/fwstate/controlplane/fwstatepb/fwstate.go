package fwstatepb

import (
	"fmt"

	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// ValidateTimeouts rejects timeout values that do not fit in
// fw_state_value::last_ttl.
func (m *SyncConfig) ValidateTimeouts() error {
	if m == nil {
		return nil
	}

	type timeoutField struct {
		name  string
		value uint64
	}

	fields := []timeoutField{
		{"tcp_syn_ack", m.GetTcpSynAck()},
		{"tcp_syn", m.GetTcpSyn()},
		{"tcp_fin", m.GetTcpFin()},
		{"tcp", m.GetTcp()},
		{"udp", m.GetUdp()},
		{"default", m.GetDefault()},
	}

	var invalid []string
	for _, field := range fields {
		if field.value > cfwstate.TTL48Max {
			invalid = append(invalid, field.name)
		}
	}

	if len(invalid) > 0 {
		return fmt.Errorf("timeout values exceed 48-bit limit: %v", invalid)
	}

	return nil
}
