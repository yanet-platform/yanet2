package netlink

import (
	"errors"
	"fmt"
	"net"

	vnetlink "github.com/vishvananda/netlink"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// LinkIdentity detects replacement during one restoration or discovery pass.
type LinkIdentity struct {
	Name            string
	Index           int
	Type            string
	ParentIndex     int
	HardwareAddress string
	Loopback        bool
	VLANID          int
	VLANProtocol    vnetlink.VlanProtocol
	TuntapMode      vnetlink.TuntapMode
}

// IdentifyLink captures stable properties without configuration or ownership.
func IdentifyLink(link vnetlink.Link) (LinkIdentity, error) {
	if link == nil || link.Attrs() == nil {
		return LinkIdentity{}, errors.New("incomplete link dump")
	}
	attributes := link.Attrs()
	if attributes.Name == "" || attributes.Index <= 0 {
		return LinkIdentity{}, fmt.Errorf("invalid link name/index %q/%d", attributes.Name, attributes.Index)
	}
	identity := LinkIdentity{
		Name: attributes.Name, Index: attributes.Index, Type: link.Type(),
		ParentIndex: attributes.ParentIndex, HardwareAddress: string(attributes.HardwareAddr),
		Loopback: attributes.Flags&net.FlagLoopback != 0,
	}
	if vlan, ok := link.(*vnetlink.Vlan); ok {
		identity.VLANID = vlan.VlanId
		identity.VLANProtocol = vlan.VlanProtocol
	}
	if tuntap, ok := link.(*vnetlink.Tuntap); ok {
		identity.TuntapMode = tuntap.Mode
	}
	return identity, nil
}

// ValidateLink rejects incompatible objects without deleting or migrating them.
func ValidateLink(wanted netplan.Link, link, parent vnetlink.Link) error {
	identity, err := IdentifyLink(link)
	if err != nil {
		return err
	}
	if identity.Name != wanted.Name {
		return fmt.Errorf("link %q resolved as %q", wanted.Name, identity.Name)
	}
	switch wanted.Kind {
	case netplan.LinkKindKNI:
		if identity.Loopback || (identity.Type != "device" &&
			(identity.Type != "tuntap" || identity.TuntapMode != vnetlink.TUNTAP_MODE_TAP)) {
			return fmt.Errorf("KNI %q has incompatible type %q", wanted.Name, identity.Type)
		}
	case netplan.LinkKindLoopback:
		if !identity.Loopback {
			return errors.New("lo is not a kernel loopback")
		}
	case netplan.LinkKindDummy:
		if identity.Type != "dummy" {
			return fmt.Errorf("dummy %q has incompatible type %q", wanted.Name, identity.Type)
		}
	case netplan.LinkKindVLAN:
		vlan, ok := link.(*vnetlink.Vlan)
		if !ok || parent == nil || parent.Attrs() == nil ||
			vlan.ParentIndex != parent.Attrs().Index || vlan.VlanId != wanted.VLANID ||
			vlan.VlanProtocol != vnetlink.VLAN_PROTOCOL_8021Q {
			return fmt.Errorf("VLAN %q has incompatible type, parent, ID or protocol", wanted.Name)
		}
	default:
		return fmt.Errorf("unsupported link kind %d", wanted.Kind)
	}
	return nil
}
