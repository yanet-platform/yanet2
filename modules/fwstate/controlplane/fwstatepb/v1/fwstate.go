package fwstatepb

import (
	"fmt"

	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

const (
	// maxSyncPort is the highest value accepted for port_multicast,
	// matching the width of the C-side uint16 port field.
	maxSyncPort uint32 = 65535

	// syncAddrLen is the width every sync address is stored at: the
	// module matches and stamps IPv6 addresses only.
	syncAddrLen = 16
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

// ValidateFields rejects sync values stated in a form the config cannot
// store, before they are merged over the values they update.
//
// An absent address and a zero port both mean "leave this as it is" in
// the merge, so only a value that is present and unusable fails here: a
// port too wide for the stored field, or an address of any width other
// than an IPv6 one. Whether the merged result names a usable destination
// is decided against the config it replaces, not here.
func (m *SyncConfig) ValidateFields() error {
	if portMulticast := m.GetPortMulticast(); portMulticast > maxSyncPort {
		return fmt.Errorf(
			"port_multicast %d exceeds maximum allowed value %d",
			portMulticast, maxSyncPort,
		)
	}
	if err := validateAddrWidth("src_addr", m.GetSrcAddr().GetAddr()); err != nil {
		return err
	}

	return validateAddrWidth("dst_addr_multicast", m.GetDstAddrMulticast().GetAddr())
}

// Validate reports whether the settings are installable as they stand,
// which is what the request merged over the config it replaces must be.
//
// Synchronization is optional. A config naming no part of the sync
// destination leaves the module a passthrough that matches no sync
// packet, which is what a first create carrying no sync settings
// installs. Naming only part of it is refused: such a module would look
// configured while matching nothing.
func (m *SyncConfig) Validate() error {
	srcAddrSet := isAddrSet(m.GetSrcAddr().GetAddr())
	multicastAddrSet := isAddrSet(m.GetDstAddrMulticast().GetAddr())
	multicastPortSet := m.GetPortMulticast() != 0

	if srcAddrSet || multicastAddrSet || multicastPortSet {
		var missing []string
		if !srcAddrSet {
			missing = append(missing, "src_addr")
		}
		if !multicastAddrSet {
			missing = append(missing, "dst_addr_multicast")
		}
		if !multicastPortSet {
			missing = append(missing, "port_multicast")
		}
		if len(missing) > 0 {
			return fmt.Errorf("missing required sync config fields: %v", missing)
		}
	}

	return m.ValidateTimeouts()
}

// validateAddrWidth rejects an address stated at a width the config
// cannot store; an absent one asks for no change and passes.
func validateAddrWidth(field string, addr []byte) error {
	if len(addr) == 0 || len(addr) == syncAddrLen {
		return nil
	}

	return fmt.Errorf(
		"%s must be a %d-byte IPv6 address, got %d bytes",
		field, syncAddrLen, len(addr),
	)
}

// isAddrSet reports whether an address is stored at full width and is
// not the all-zero one that stands for unset.
func isAddrSet(addr []byte) bool {
	return len(addr) == syncAddrLen && !isAllZeroBytes(addr)
}

// isAllZeroBytes reports whether every byte of the slice is zero.
func isAllZeroBytes(b []byte) bool {
	for _, value := range b {
		if value != 0 {
			return false
		}
	}

	return true
}
