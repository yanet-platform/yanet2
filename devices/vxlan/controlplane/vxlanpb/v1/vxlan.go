package vxlanpb

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// maxVNI is the largest 24-bit VXLAN network identifier of RFC 7348.
const maxVNI = 1<<24 - 1

// Validate checks the device name, its pipeline bindings and the required
// tunnel.
func (m *UpdateDeviceVxlanRequest) Validate() error {
	if err := commonpb.ValidateDeviceName("name", m.GetName()); err != nil {
		return err
	}
	if err := m.GetDevice().Validate(); err != nil {
		return fmt.Errorf("device: %w", err)
	}
	if m.GetTunnel() == nil {
		return errors.New("tunnel is required")
	}
	if err := m.GetTunnel().Validate(); err != nil {
		return fmt.Errorf("tunnel: %w", err)
	}
	return nil
}

// Validate checks the device name.
func (m *ShowDeviceVxlanRequest) Validate() error {
	return commonpb.ValidateDeviceName("name", m.GetName())
}

// Validate checks that both endpoints are unicast, both hardware addresses
// are nonzero unicast and the VNI fits 24 bits.
func (m *VxlanTunnel) Validate() error {
	if err := validateUnicastIPv4("local_ip", m.GetLocalIp()); err != nil {
		return err
	}
	if err := validateUnicastIPv4("remote_ip", m.GetRemoteIp()); err != nil {
		return err
	}
	if err := validateUnicastMAC("local_mac", m.GetLocalMac()); err != nil {
		return err
	}
	if err := validateUnicastMAC("remote_mac", m.GetRemoteMac()); err != nil {
		return err
	}
	if vni := m.GetVni(); vni > maxVNI {
		return fmt.Errorf("vni %d must be in range 0..%d", vni, maxVNI)
	}
	return nil
}

func validateUnicastIPv4(field string, addr *commonpb.IPv4Address) error {
	if addr == nil {
		return fmt.Errorf("%s is required", field)
	}

	ip := addr.ToAddr()
	if ip.IsUnspecified() || ip.IsMulticast() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return fmt.Errorf("%s %s must be a unicast address", field, ip)
	}
	return nil
}

func validateUnicastMAC(field string, mac *commonpb.MACAddress) error {
	if mac == nil {
		return fmt.Errorf("%s is required", field)
	}
	if mac.GetAddr()>>48 != 0 {
		return fmt.Errorf("%s upper 16 bits must be zero", field)
	}

	octets := mac.EUI48()
	if octets == [6]byte{} || octets[0]&1 != 0 {
		return fmt.Errorf("%s %s must be a nonzero unicast address", field, net.HardwareAddr(octets[:]))
	}
	return nil
}
