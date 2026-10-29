//go:build sdk_rust_fixtures

package cfixture

/*
#include <stdlib.h>
#include "api/agent.h"
#include "common/network.h"
typedef struct net4 net4;
#include "lib/classify/rule.h"
#include "yanet_sdk_fixture_api.h"
struct cp_module *yanet_cp_yanet_sdk_fixture_api__firewall_build(
 struct agent *, const char *, const struct Rule *, size_t,
 const struct DeviceName *, size_t, const struct net4 *, size_t,
 const struct net6 *, size_t, const struct filter_port_range *, size_t,
 const struct classify_line_range *, size_t, yanet_error **);
int yanet_cp_yanet_sdk_fixture_api__firewall_free(struct cp_module *, yanet_error **);
struct cp_module *yanet_cp_yanet_sdk_fixture_api__firewall_faults_build(
 struct agent *, const char *, const struct Rule *, size_t,
 const struct DeviceName *, size_t, const struct net4 *, size_t,
 const struct net6 *, size_t, const struct filter_port_range *, size_t,
 const struct classify_line_range *, size_t, yanet_error **);
struct cp_module *yanet_cp_yanet_sdk_fixture_api__firewall_faults_names_drop(
 struct agent *, const char *, const struct Rule *, size_t,
 const struct DeviceName *, size_t, const struct net4 *, size_t,
 const struct net6 *, size_t, const struct filter_port_range *, size_t,
 const struct classify_line_range *, size_t, yanet_error **);
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

// FirewallInput owns the borrowed-view records prepared before compilation.
// Its zero value is ready for rules to be added.
type FirewallInput struct {
	rules   []C.struct_Rule
	devices []C.struct_DeviceName
	nets4   []C.struct_net4
	nets6   []C.struct_net6
	ports   []C.struct_filter_port_range
	lines   []C.struct_classify_line_range
}

func view(offset, count int) C.struct_Span {
	return C.struct_Span{offset: C.uint64_t(offset), count: C.uint64_t(count)}
}

// Add appends one rule after validating every fallible conversion.
func (m *FirewallInput) Add(rule cacl.ACLRule) error {
	var record C.struct_Rule
	if len(rule.Actions) > len(record.actions) {
		return fmt.Errorf("too many firewall actions")
	}
	record.action_count = C.uint64_t(len(rule.Actions))
	for idx, action := range rule.Actions {
		if action.Kind > 255 {
			return fmt.Errorf("firewall action kind %d does not fit in a byte", action.Kind)
		}
		record.actions[idx] = C.uint8_t(action.Kind)
	}
	if err := setName(bytesOf(&record.counter), rule.Counter); err != nil {
		return err
	}
	names := make([]C.struct_DeviceName, len(rule.Devices))
	for idx, device := range rule.Devices {
		if err := setName(bytesOf(&names[idx].name), device.Name); err != nil {
			return err
		}
	}
	record.devices = view(len(m.devices), len(names))
	m.devices = append(m.devices, names...)
	record.src4 = m.addNet4(rule.Src4s)
	record.dst4 = m.addNet4(rule.Dst4s)
	record.src6 = m.addNet6(rule.Src6s)
	record.dst6 = m.addNet6(rule.Dst6s)
	record.src_ports = m.addPorts(rule.SrcPortRanges)
	record.dst_ports = m.addPorts(rule.DstPortRanges)
	split := cacl.SplitProtoRanges(rule.ProtoRanges)
	record.protocols = m.addLines(split.IpProto)
	record.tcp_flags = m.addLines(split.TCPFlags)
	record.icmp_types = m.addLines(split.ICMPTypes)
	paths := cacl.RulePathMembership(rule.ProtoRanges, rule.SrcPortRanges, rule.DstPortRanges, rule.Fragment)
	if paths.Plain {
		record.paths |= C.FIREWALL_PATH_PLAIN
	}
	if paths.TCP {
		record.paths |= C.FIREWALL_PATH_TCP
	}
	if paths.UDP {
		record.paths |= C.FIREWALL_PATH_UDP
	}
	if paths.ICMP {
		record.paths |= C.FIREWALL_PATH_ICMP
	}
	record.fragment = C.uint64_t(rule.Fragment)
	m.rules = append(m.rules, record)
	return nil
}
func (m *FirewallInput) addNet4(networks []xnetip.Contiguous[xnetip.Network4]) C.struct_Span {
	span := view(len(m.nets4), len(networks))
	for _, network := range networks {
		var record C.struct_net4
		address := network.Network().Addr().As4()
		mask := network.Network().Mask().As4()
		copy(bytesOf(&record.addr), address[:])
		copy(bytesOf(&record.mask), mask[:])
		m.nets4 = append(m.nets4, record)
	}
	return span
}
func (m *FirewallInput) addNet6(networks []xnetip.BiContiguous) C.struct_Span {
	span := view(len(m.nets6), len(networks))
	for _, network := range networks {
		var record C.struct_net6
		address := network.Network().Addr().As16()
		mask := network.Network().Mask().As16()
		copy(bytesOf(&record.addr), address[:])
		copy(bytesOf(&record.mask), mask[:])
		m.nets6 = append(m.nets6, record)
	}
	return span
}
func (m *FirewallInput) addPorts(ranges filter.PortRanges) C.struct_Span {
	span := view(len(m.ports), len(ranges))
	for _, interval := range ranges {
		m.ports = append(m.ports, C.struct_filter_port_range{from: C.uint16_t(interval.From), to: C.uint16_t(interval.To)})
	}
	return span
}
func (m *FirewallInput) addLines(ranges []cacl.LineRange) C.struct_Span {
	span := view(len(m.lines), len(ranges))
	for _, interval := range ranges {
		m.lines = append(m.lines, C.struct_classify_line_range{from: C.uint32_t(interval.From), to: C.uint32_t(interval.To)})
	}
	return span
}

// NewFirewallConfig compiles the prepared input through the authored Rust API.
func NewFirewallConfig(agent *ffi.Agent, name string, input *FirewallInput) (*ModuleConfig, error) {
	return buildFirewall(agent, name, input, 0)
}

// NewFirewallLateFault injects an error at the final projection.
//
// Preceding native tables are already complete when its getter fails.
func NewFirewallLateFault(agent *ffi.Agent, name string, input *FirewallInput) (*ModuleConfig, error) {
	return buildFirewall(agent, name, input, 1)
}

// NewFirewallNamesDrop exercises destruction of a completed name reader.
func NewFirewallNamesDrop(agent *ffi.Agent, name string, input *FirewallInput) (*ModuleConfig, error) {
	return buildFirewall(agent, name, input, 2)
}
func buildFirewall(agent *ffi.Agent, name string, input *FirewallInput, mode uint8) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var pinner runtime.Pinner
	defer pinner.Unpin()
	rules, ruleCount := first(&pinner, input.rules)
	devices, deviceCount := first(&pinner, input.devices)
	nets4, net4Count := first(&pinner, input.nets4)
	nets6, net6Count := first(&pinner, input.nets6)
	ports, portCount := first(&pinner, input.ports)
	lines, lineCount := first(&pinner, input.lines)
	var chain *C.yanet_error
	var module *C.struct_cp_module
	if mode == 1 {
		module = C.yanet_cp_yanet_sdk_fixture_api__firewall_faults_build(
			(*C.struct_agent)(agent.AsRawPtr()),
			cName,
			rules,
			ruleCount,
			devices,
			deviceCount,
			nets4,
			net4Count,
			nets6,
			net6Count,
			ports,
			portCount,
			lines,
			lineCount,
			&chain,
		)
	} else if mode == 2 {
		module = C.yanet_cp_yanet_sdk_fixture_api__firewall_faults_names_drop(
			(*C.struct_agent)(agent.AsRawPtr()),
			cName,
			rules,
			ruleCount,
			devices,
			deviceCount,
			nets4,
			net4Count,
			nets6,
			net6Count,
			ports,
			portCount,
			lines,
			lineCount,
			&chain,
		)
	} else {
		module = C.yanet_cp_yanet_sdk_fixture_api__firewall_build(
			(*C.struct_agent)(agent.AsRawPtr()),
			cName,
			rules,
			ruleCount,
			devices,
			deviceCount,
			nets4,
			net4Count,
			nets6,
			net6Count,
			ports,
			portCount,
			lines,
			lineCount,
			&chain,
		)
	}
	if module == nil {
		return nil, fmt.Errorf("failed to build firewall config: %w", cerrors.FromC(unsafe.Pointer(chain)))
	}
	return &ModuleConfig{ptr: ffi.NewModuleConfig(unsafe.Pointer(module)), free: freeFirewall}, nil
}
func freeFirewall(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
	var chain *C.yanet_error
	status, errno := C.yanet_cp_yanet_sdk_fixture_api__firewall_free((*C.struct_cp_module)(ptr), &chain)
	return int(status), unsafe.Pointer(chain), errno
}
