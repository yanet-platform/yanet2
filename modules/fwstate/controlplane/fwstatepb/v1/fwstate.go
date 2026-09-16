package fwstatepb

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

const (
	// maxSyncPort is the highest value accepted for either sync port,
	// matching the width of the C-side uint16 port fields.
	maxSyncPort uint32 = 65535

	// syncAddrLen is the width every sync address is stored at: the
	// module matches and stamps IPv6 addresses only.
	syncAddrLen = 16
)

// Validate checks that an update contains only request-local values.
func (m *UpdateConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	updateMask := m.GetUpdateMask()
	mapNameV4Selected := false
	mapNameV6Selected := false
	if updateMask == nil {
		if err := m.GetSyncConfig().ValidateFields(); err != nil {
			return fmt.Errorf("sync_config: %w", err)
		}
		if err := m.ValidateEndpointClears(); err != nil {
			return fmt.Errorf("invalid sync endpoint update: %w", err)
		}
	} else {
		if m.GetClearMulticast() || m.GetClearUnicast() {
			return errors.New("endpoint clear flags cannot be combined with an update mask")
		}

		for _, path := range updateMask.GetPaths() {
			switch path {
			case "map_name_v4":
				mapNameV4Selected = true
			case "map_name_v6":
				mapNameV6Selected = true
			case "sync_config.dst_ether", "sync_config.dst_addr_unicast",
				"sync_config.port_unicast", "sync_config.src_addr",
				"sync_config.dst_addr_multicast", "sync_config.port_multicast",
				"sync_config.tcp_syn_ack", "sync_config.tcp_syn",
				"sync_config.tcp_fin", "sync_config.tcp",
				"sync_config.udp", "sync_config.default",
				"sync_config.sync_suppress_timeout":
			default:
				return fmt.Errorf("unknown update mask path %q", path)
			}
		}
	}

	if updateMask == nil || mapNameV4Selected {
		if err := validateOptionalMapName("map_name_v4", m.GetMapNameV4()); err != nil {
			return err
		}
	}
	if updateMask == nil || mapNameV6Selected {
		if err := validateOptionalMapName("map_name_v6", m.GetMapNameV6()); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks that a show request names a configuration.
func (m *ShowConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

// Validate checks that a delete request names a configuration.
func (m *DeleteConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
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

// ValidateFields rejects sync values stated in a form the config cannot store.
//
// Empty endpoints are representable. Whether they preserve existing values
// or clear them depends on the update contract. A stated destination address
// requires a port; the merged configuration must also be usable.
func (m *SyncConfig) ValidateFields() error {
	if m == nil {
		return nil
	}

	if portMulticast := m.GetPortMulticast(); portMulticast > maxSyncPort {
		return fmt.Errorf(
			"port_multicast %d exceeds maximum allowed value %d",
			portMulticast, maxSyncPort,
		)
	}
	if portUnicast := m.GetPortUnicast(); portUnicast > maxSyncPort {
		return fmt.Errorf(
			"port_unicast %d exceeds maximum allowed value %d",
			portUnicast, maxSyncPort,
		)
	}
	if err := validateAddrWidth("src_addr", m.GetSrcAddr().GetAddr()); err != nil {
		return err
	}
	if err := validateAddrWidth("dst_addr_multicast", m.GetDstAddrMulticast().GetAddr()); err != nil {
		return err
	}
	if err := validateAddrWidth("dst_addr_unicast", m.GetDstAddrUnicast().GetAddr()); err != nil {
		return err
	}
	if dstEther := m.GetDstEther(); dstEther != nil && dstEther.GetAddr()>>48 != 0 {
		return fmt.Errorf("dst_ether must be an EUI-48 address")
	}

	// A supplied destination address needs a port. Masked requests reach
	// this check after merging, so the port may come from the stored config.
	if len(m.GetDstAddrMulticast().GetAddr()) != 0 && m.GetPortMulticast() == 0 {
		return fmt.Errorf("port_multicast is required with dst_addr_multicast")
	}
	if len(m.GetDstAddrUnicast().GetAddr()) != 0 && m.GetPortUnicast() == 0 {
		return fmt.Errorf("port_unicast is required with dst_addr_unicast")
	}

	return nil
}

// ValidateEndpointClears rejects an update that asks to clear an endpoint
// while also supplying fields for that same endpoint.
func (m *UpdateConfigRequest) ValidateEndpointClears() error {
	syncConfig := m.GetSyncConfig()
	if m == nil || syncConfig == nil {
		return nil
	}

	if m.GetClearMulticast() && endpointFieldsSet(
		syncConfig.GetDstAddrMulticast().GetAddr(),
		syncConfig.GetPortMulticast(),
	) {
		return fmt.Errorf("clear_multicast cannot be combined with multicast endpoint fields")
	}
	if m.GetClearUnicast() && endpointFieldsSet(
		syncConfig.GetDstAddrUnicast().GetAddr(),
		syncConfig.GetPortUnicast(),
	) {
		return fmt.Errorf("clear_unicast cannot be combined with unicast endpoint fields")
	}

	return nil
}

func endpointFieldsSet(addr []byte, port uint32) bool {
	return len(addr) != 0 || port != 0
}

// ValidateMerged reports whether the settings are installable as they stand,
// which is what the request merged over the config it replaces must be.
//
// Synchronization is optional. A config naming no destination leaves external
// traffic in ordinary processing, while trusted internal events are consumed
// and dropped. Naming only part of an endpoint is refused because it cannot
// be matched or emitted safely.
func (m *SyncConfig) ValidateMerged() error {
	if m == nil {
		return nil
	}

	if err := validateAddrWidth("src_addr", m.GetSrcAddr().GetAddr()); err != nil {
		return err
	}
	if err := validateAddrWidth("dst_addr_multicast", m.GetDstAddrMulticast().GetAddr()); err != nil {
		return err
	}
	if err := validateAddrWidth("dst_addr_unicast", m.GetDstAddrUnicast().GetAddr()); err != nil {
		return err
	}
	if dstEther := m.GetDstEther(); dstEther != nil && dstEther.GetAddr()>>48 != 0 {
		return fmt.Errorf("dst_ether must be an EUI-48 address")
	}

	srcAddrSet := isAddrSet(m.GetSrcAddr().GetAddr())
	multicastAddrSet := isAddrSet(m.GetDstAddrMulticast().GetAddr())
	multicastPortSet := m.GetPortMulticast() != 0
	unicastAddrSet := isAddrSet(m.GetDstAddrUnicast().GetAddr())
	unicastPortSet := m.GetPortUnicast() != 0

	// A zero address and zero port mean that endpoint is disabled. This is
	// also the representation produced for a config created without sync
	// settings, so external traffic remains ordinary while trusted internal
	// events are consumed and dropped.
	multicastConfigured := multicastAddrSet || multicastPortSet
	unicastConfigured := unicastAddrSet || unicastPortSet
	if !multicastConfigured && !unicastConfigured {
		return m.ValidateTimeouts()
	}

	var missing []string
	if !srcAddrSet {
		missing = append(missing, "src_addr")
	}
	if dstEther := m.GetDstEther(); dstEther == nil || dstEther.GetAddr()&macAddrMask == 0 {
		missing = append(missing, "dst_ether")
	}
	if multicastConfigured {
		if !multicastAddrSet {
			missing = append(missing, "dst_addr_multicast")
		}
		if !multicastPortSet {
			missing = append(missing, "port_multicast")
		}
	}
	if unicastConfigured {
		if !unicastAddrSet {
			missing = append(missing, "dst_addr_unicast")
		}
		if !unicastPortSet {
			missing = append(missing, "port_unicast")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required sync config fields: %v", missing)
	}

	if unicastAddrSet {
		unicast := netip.AddrFrom16([16]byte(m.GetDstAddrUnicast().GetAddr()))
		if unicast.IsMulticast() {
			return fmt.Errorf("dst_addr_unicast must not be a multicast address")
		}
	}

	return m.ValidateTimeouts()
}

const macAddrMask uint64 = (1 << 48) - 1

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
