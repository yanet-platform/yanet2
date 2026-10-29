package fwstatepb

import (
	"errors"
	"fmt"

	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

const (
	// TTL48Max is the largest timeout value storable in the 48-bit field.
	TTL48Max uint64 = (1 << 48) - 1

	// maxSyncPort is the highest value accepted for a sync port,
	// matching the width of the C-side uint16 port fields.
	maxSyncPort uint32 = 65535

	// MaxSyncEndpoints is the largest number of endpoints one config
	// carries. It mirrors the C FWSTATE_SYNC_ENDPOINTS_MAX bound of the
	// endpoint array in the shared-memory config.
	MaxSyncEndpoints = 8

	// syncAddrLen is the width every sync address is stored at: the
	// module matches and stamps IPv6 addresses only.
	syncAddrLen = 16

	// ipv6HeaderLen and udpHeaderLen are the wire headers the sync MTU
	// counts in front of the frames.
	ipv6HeaderLen uint32 = 40
	udpHeaderLen  uint32 = 8

	// syncFrameLen is the wire size of one sync frame.
	syncFrameLen uint32 = 56

	// MinSyncMTU is the smallest nonzero sync MTU: the IPv6 and UDP
	// headers plus one frame. It mirrors the C FWSTATE_SYNC_MIN_MTU.
	MinSyncMTU = ipv6HeaderLen + udpHeaderLen + syncFrameLen

	// maxSyncMTU matches the width of the C-side uint16 sync_mtu field.
	maxSyncMTU uint32 = 65535
)

// Validate checks the values an update carries.
//
// Whether the merged config is usable depends on the stored one and is
// checked by the service.
func (m *UpdateConfigRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}
	if err := validateOptionalMapName("map_name_v4", m.GetMapNameV4()); err != nil {
		return err
	}
	if err := validateOptionalMapName("map_name_v6", m.GetMapNameV6()); err != nil {
		return err
	}
	if err := m.GetSyncConfig().ValidateFields(); err != nil {
		return fmt.Errorf("sync_config: %w", err)
	}

	return nil
}

// Validate checks that a show request names a configuration.
func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

// Validate checks that a delete request names a configuration.
func (m *DeleteConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func validateOptionalMapName(field, name string) error {
	if name == "" {
		return nil
	}
	return fwstatemappb.ValidateMapNameField(field, name)
}

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
		// Guard against uint64 wraparound: compare the delta instead of
		// summing, so a huge suppress window cannot slip past the limit.
		if suppress > TTL48Max || field.Value > TTL48Max-suppress {
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

// validateSyncEndpoint checks one update-carried endpoint's form.
func validateSyncEndpoint(idx int, endpoint *SyncEndpoint) error {
	if err := validateAddrWidth(
		fmt.Sprintf("endpoints[%d].src_addr", idx),
		endpoint.GetSrcAddr().GetAddr(),
	); err != nil {
		return err
	}
	if err := validateAddrWidth(
		fmt.Sprintf("endpoints[%d].dst_addr", idx),
		endpoint.GetDstAddr().GetAddr(),
	); err != nil {
		return err
	}
	if port := endpoint.GetPort(); port == 0 || port > maxSyncPort {
		return fmt.Errorf(
			"endpoints[%d].port must be in 1..%d, got %d",
			idx, maxSyncPort, port,
		)
	}
	return nil
}

// validateEndpointList checks the endpoint list's form an update states.
func (m *SyncConfig) validateEndpointList() error {
	if m.GetNoEndpoints() && len(m.GetEndpoints()) > 0 {
		return errors.New("no_endpoints cannot be combined with endpoints")
	}
	if len(m.GetEndpoints()) > MaxSyncEndpoints {
		return fmt.Errorf(
			"at most %d sync endpoints are supported, got %d",
			MaxSyncEndpoints, len(m.GetEndpoints()),
		)
	}
	for idx, endpoint := range m.GetEndpoints() {
		if err := validateSyncEndpoint(idx, endpoint); err != nil {
			return err
		}
	}
	return nil
}

// ValidateFields rejects sync values an update states in a form the config
// cannot store.
//
// Every endpoint an update carries must be complete: full-width source and
// destination addresses and a port in 1..65535.
func (m *SyncConfig) ValidateFields() error {
	if m == nil {
		return nil
	}

	if err := validateSyncMTU(m.GetSyncMtu()); err != nil {
		return err
	}

	timeoutFields := []struct {
		Name  string
		Value uint64
	}{
		{"tcp_syn_ack", m.GetTcpSynAck()},
		{"tcp_syn", m.GetTcpSyn()},
		{"tcp_fin", m.GetTcpFin()},
		{"tcp", m.GetTcp()},
		{"udp", m.GetUdp()},
		{"default", m.GetDefault()},
		{"sync_suppress_timeout", m.GetSyncSuppressTimeout()},
	}
	for _, field := range timeoutFields {
		if field.Value > TTL48Max {
			return fmt.Errorf(
				"%s %d exceeds maximum allowed value %d",
				field.Name, field.Value, TTL48Max,
			)
		}
	}

	return m.validateEndpointList()
}

// ValidateMerged reports whether the settings are installable as they stand,
// which is what the request merged over the config it replaces must be.
//
// Synchronization is optional. A config naming no endpoint leaves external
// traffic in ordinary processing, while locally stashed events are applied
// without emission. An endpoint naming only part of its source, destination
// and port is refused because it cannot be matched or emitted safely.
func (m *SyncConfig) ValidateMerged() error {
	if m == nil {
		return nil
	}

	if err := m.validateEndpointList(); err != nil {
		return err
	}

	var missing []string
	for idx, endpoint := range m.GetEndpoints() {
		if !isAddrSet(endpoint.GetSrcAddr().GetAddr()) {
			missing = append(missing, fmt.Sprintf("endpoints[%d].src_addr", idx))
		}
		if !isAddrSet(endpoint.GetDstAddr().GetAddr()) {
			missing = append(missing, fmt.Sprintf("endpoints[%d].dst_addr", idx))
		}
		if endpoint.GetPort() == 0 {
			missing = append(missing, fmt.Sprintf("endpoints[%d].port", idx))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required sync config fields: %v", missing)
	}

	return m.ValidateTimeouts()
}

// validateSyncMTU accepts zero, which selects the default, and any value
// that holds one frame and fits the C field.
func validateSyncMTU(mtu uint32) error {
	if mtu == 0 {
		return nil
	}
	if mtu < MinSyncMTU {
		return fmt.Errorf("sync_mtu %d is below the minimum %d", mtu, MinSyncMTU)
	}
	if mtu > maxSyncMTU {
		return fmt.Errorf("sync_mtu %d exceeds maximum allowed value %d", mtu, maxSyncMTU)
	}
	return nil
}

// validateAddrWidth accepts empty addresses and full-width IPv6 addresses.
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
