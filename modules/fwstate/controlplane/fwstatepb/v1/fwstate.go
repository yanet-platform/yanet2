package fwstatepb

import (
	"fmt"

	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

const (
	// maxSyncPort is the highest value accepted for port_multicast and
	// port_unicast, matching the width of the C-side uint16 port field.
	maxSyncPort uint32 = 65535
)

// ValidateTimeouts rejects timeout values that do not fit in
// fw_state_value::last_ttl.
func (m *SyncConfig) ValidateTimeouts() error {
	if m == nil {
		return nil
	}

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
		}
	}

	if len(invalid) > 0 {
		return fmt.Errorf("timeout values exceed 48-bit limit: %v", invalid)
	}

	return nil
}

// ValidatePorts rejects sync config ports that do not fit into the C-side
// uint16 port field.
//
// A zero port means "unset / keep current" and is allowed here. The
// required-destination-pair check in Validate rejects a request that leaves
// both destinations unset.
func (m *SyncConfig) ValidatePorts() error {
	if m == nil {
		return nil
	}
	if portMulticast := m.GetPortMulticast(); portMulticast > maxSyncPort {
		return fmt.Errorf("port_multicast %d exceeds maximum allowed value %d", portMulticast, maxSyncPort)
	}
	if portUnicast := m.GetPortUnicast(); portUnicast > maxSyncPort {
		return fmt.Errorf("port_unicast %d exceeds maximum allowed value %d", portUnicast, maxSyncPort)
	}
	return nil
}

// Validate reports whether the sync config is publishable: ports fit the
// C-side uint16 field, the required source address, destination MAC, and at
// least one destination address+port pair are set, and timeouts are bounded.
//
// ACL configs with a sync_config delegate here so both modules enforce
// identical constraints.
func (m *SyncConfig) Validate() error {
	if m == nil {
		return fmt.Errorf("sync config is required")
	}

	if err := m.ValidatePorts(); err != nil {
		return err
	}

	var missing []string

	if len(m.GetSrcAddr().GetAddr()) != 16 || isAllZeroBytes(m.GetSrcAddr().GetAddr()) {
		missing = append(missing, "src_addr")
	}

	dstEther := m.GetDstEther()
	if dstEther == nil {
		missing = append(missing, "dst_ether")
	} else {
		eui := dstEther.EUI48()
		if isAllZeroBytes(eui[:]) {
			missing = append(missing, "dst_ether")
		}
	}

	hasMulticast := len(m.GetDstAddrMulticast().GetAddr()) == 16 && !isAllZeroBytes(m.GetDstAddrMulticast().GetAddr()) && m.PortMulticast != 0
	hasUnicast := len(m.GetDstAddrUnicast().GetAddr()) == 16 && !isAllZeroBytes(m.GetDstAddrUnicast().GetAddr()) && m.PortUnicast != 0

	if !hasMulticast && !hasUnicast {
		missing = append(missing, "dst_addr_multicast+port_multicast or dst_addr_unicast+port_unicast")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required sync config fields: %v", missing)
	}

	if err := m.ValidateTimeouts(); err != nil {
		return fmt.Errorf("invalid sync config timeouts: %w", err)
	}

	return nil
}

// isAllZeroBytes reports whether every byte in the slice is zero.
func isAllZeroBytes(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
