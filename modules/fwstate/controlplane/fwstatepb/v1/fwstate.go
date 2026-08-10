package fwstatepb

import (
	"fmt"

	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// ValidateTimeouts rejects timeout values that do not fit in
// fw_state_value::last_ttl.
//
// Because the dataplane inflates every entry TTL by sync_suppress_timeout
// (effective keep-alive), each configured timeout plus the suppress window
// must still fit in the 48-bit last_ttl field, otherwise it would be silently
// truncated on store.
func (m *SyncConfig) ValidateTimeouts() error {
	if m == nil {
		return nil
	}

	suppress := m.GetSyncSuppressTimeout()

	type timeoutField struct {
		Name  string
		Value uint64
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
		if field.Value > cfwstate.TTL48Max {
			invalid = append(invalid, field.Name)
			continue
		}
		// Guard against uint64 wraparound: compare the delta instead of
		// summing, so a huge suppress window cannot slip past the limit.
		if suppress > cfwstate.TTL48Max-field.Value {
			invalid = append(
				invalid,
				fmt.Sprintf("%s+sync_suppress_timeout", field.Name),
			)
		}
	}

	if len(invalid) > 0 {
		return fmt.Errorf("timeout values exceed 48-bit limit: %v", invalid)
	}

	return nil
}
