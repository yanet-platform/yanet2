//go:build sdk_rust_fixtures

package cfixture

/*
#include <stdlib.h>

#include "api/agent.h"
#include "common/filter_views.h"
#include "common/network.h"
#include "lib/classify/rule.h"
#include "yanet_sdk_fixture_api.h"

struct cp_module *
yanet_cp_yanet_sdk_fixture_api__firewall_build(
	struct agent *agent, const char *name,
	const struct Rule *rules, size_t rules_len,
	const struct DeviceName *devices, size_t devices_len,
	const struct net4 *nets4, size_t nets4_len,
	const struct net6 *nets6, size_t nets6_len,
	const struct filter_port_range *ports, size_t ports_len,
	const struct classify_line_range *lines, size_t lines_len,
	yanet_error **err
);

int
yanet_cp_yanet_sdk_fixture_api__firewall_free(
	struct cp_module *module, yanet_error **err
);
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// The records of a firewall build: its rules and their devices, and the C
// structs of the classification headers the network, port and protocol sets
// of a rule are runs of.
type (
	rule       = C.struct_Rule
	deviceName = C.struct_DeviceName
	span       = C.struct_Span
	net4       = C.struct_net4
	net6       = C.struct_net6
	portRange  = C.struct_filter_port_range
	lineRange  = C.struct_classify_line_range
)

// FirewallInput is what a firewall configuration is built from: the rules in
// order, and the arrays their sets are runs of.
type FirewallInput struct {
	rules   []rule
	devices []deviceName
	nets4   []net4
	nets6   []net6
	ports   []portRange
	lines   []lineRange
}

// Add appends a rule of the C ACL module, prepared as its control plane
// prepares one: the protocol ranges split into lines and the paths decided.
//
// It returns an error when the rule has more actions than a target holds, or
// when the counter or a device name holds a NUL or does not fit its field.
func (m *FirewallInput) Add(aclRule cacl.ACLRule) error {
	split := cacl.SplitProtoRanges(aclRule.ProtoRanges)
	paths := cacl.RulePathMembership(
		aclRule.ProtoRanges,
		aclRule.SrcPortRanges,
		aclRule.DstPortRanges,
		aclRule.Fragment,
	)

	var record rule
	if len(aclRule.Actions) > len(record.actions) {
		return fmt.Errorf("%d actions do not fit %d", len(aclRule.Actions), len(record.actions))
	}
	// The kinds of the api rule are the dataplane action kinds of the C ACL
	// (ACL_RULE_ACTION_KIND_* and ACTION_* both count from allow), which the
	// firewall's FIREWALL_ACTION_* follow.
	for idx, action := range aclRule.Actions {
		record.actions[idx] = C.uint8_t(action.Kind)
	}
	record.action_count = C.uint64_t(len(aclRule.Actions))
	if err := setName(bytesOf(&record.counter), aclRule.Counter); err != nil {
		return fmt.Errorf("counter name: %w", err)
	}
	devices, err := m.addDevices(aclRule.Devices)
	if err != nil {
		return err
	}
	record.devices = devices
	record.src4 = m.addNets4(aclRule.Src4s)
	record.dst4 = m.addNets4(aclRule.Dst4s)
	record.src6 = m.addNets6(aclRule.Src6s)
	record.dst6 = m.addNets6(aclRule.Dst6s)
	record.src_ports = m.addPorts(aclRule.SrcPortRanges)
	record.dst_ports = m.addPorts(aclRule.DstPortRanges)
	record.protocols = m.addLines(split.IpProto)
	record.tcp_flags = m.addLines(split.TCPFlags)
	record.icmp_types = m.addLines(split.ICMPTypes)
	for _, path := range []struct {
		taken bool
		bit   C.uint64_t
	}{
		{paths.Plain, C.FIREWALL_PATH_PLAIN},
		{paths.TCP, C.FIREWALL_PATH_TCP},
		{paths.UDP, C.FIREWALL_PATH_UDP},
		{paths.ICMP, C.FIREWALL_PATH_ICMP},
	} {
		if path.taken {
			record.paths |= path.bit
		}
	}
	switch aclRule.Fragment {
	case filter.FragmentNone:
		record.fragment = C.FIREWALL_FRAGMENT_WHOLE
	case filter.FragmentFrag:
		record.fragment = C.FIREWALL_FRAGMENT_ONLY
	default:
		record.fragment = C.FIREWALL_FRAGMENT_ANY
	}
	m.rules = append(m.rules, record)
	return nil
}

// newSpan returns the run of the count items from offset.
func newSpan(offset, count int) span {
	return span{offset: C.uint64_t(offset), count: C.uint64_t(count)}
}

func (m *FirewallInput) addDevices(devices filter.Devices) (span, error) {
	offset := len(m.devices)
	for _, device := range devices {
		var record deviceName
		if err := setName(bytesOf(&record.name), device.Name); err != nil {
			return span{}, fmt.Errorf("device name: %w", err)
		}
		m.devices = append(m.devices, record)
	}
	return newSpan(offset, len(devices)), nil
}

func (m *FirewallInput) addNets4(networks []xnetip.Contiguous[xnetip.Network4]) span {
	offset := len(m.nets4)
	for _, network := range networks {
		var record net4
		addr, mask := network.Network().Addr().As4(), network.Network().Mask().As4()
		copy(bytesOf(&record.addr), addr[:])
		copy(bytesOf(&record.mask), mask[:])
		m.nets4 = append(m.nets4, record)
	}
	return newSpan(offset, len(networks))
}

func (m *FirewallInput) addNets6(networks []xnetip.BiContiguous) span {
	offset := len(m.nets6)
	for _, network := range networks {
		var record net6
		addr, mask := network.Network().Addr().As16(), network.Network().Mask().As16()
		copy(bytesOf(&record.addr), addr[:])
		copy(bytesOf(&record.mask), mask[:])
		m.nets6 = append(m.nets6, record)
	}
	return newSpan(offset, len(networks))
}

func (m *FirewallInput) addPorts(ranges filter.PortRanges) span {
	offset := len(m.ports)
	for _, item := range ranges {
		m.ports = append(m.ports, portRange{from: C.uint16_t(item.From), to: C.uint16_t(item.To)})
	}
	return newSpan(offset, len(ranges))
}

func (m *FirewallInput) addLines(ranges []cacl.LineRange) span {
	offset := len(m.lines)
	for _, item := range ranges {
		m.lines = append(m.lines, lineRange{from: C.uint32_t(item.From), to: C.uint32_t(item.To)})
	}
	return newSpan(offset, len(ranges))
}

// NewFirewallConfig builds a firewall configuration named name from the input
// in the memory of the agent.
func NewFirewallConfig(agent *ffi.Agent, name string, input *FirewallInput) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var pinner runtime.Pinner
	defer pinner.Unpin()
	rules, rulesLen := first(&pinner, input.rules)
	devices, devicesLen := first(&pinner, input.devices)
	nets4, nets4Len := first(&pinner, input.nets4)
	nets6, nets6Len := first(&pinner, input.nets6)
	ports, portsLen := first(&pinner, input.ports)
	lines, linesLen := first(&pinner, input.lines)

	var cErr *C.yanet_error
	ptr := C.yanet_cp_yanet_sdk_fixture_api__firewall_build(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		rules,
		rulesLen,
		devices,
		devicesLen,
		nets4,
		nets4Len,
		nets6,
		nets6Len,
		ports,
		portsLen,
		lines,
		linesLen,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to build firewall config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
		free: freeFirewall,
	}, nil
}

func freeFirewall(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
	var cErr *C.yanet_error
	rc, errno := C.yanet_cp_yanet_sdk_fixture_api__firewall_free((*C.struct_cp_module)(ptr), &cErr)
	return int(rc), unsafe.Pointer(cErr), errno
}
