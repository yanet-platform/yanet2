//go:build sdk_rust_fixtures

package cfixture

/*
#include <stdlib.h>

#include "api/agent.h"
#include "common/filter_views.h"
#include "common/network.h"
#include "lib/classify/rule.h"
#include "lib/errors/errors.h"
#include "yanet_sdk_fixture_api.h"

struct cp_module *
yanet_cp_yanet_sdk_fixture_api__assign_build(
	struct agent *agent, const char *name,
	const struct AssignRule *rules, size_t rules_len,
	const struct net4 *nets4, size_t nets4_len,
	const struct net6 *nets6, size_t nets6_len,
	const struct classify_line_range *lines, size_t lines_len,
	const struct filter_port_range *ports, size_t ports_len,
	yanet_error **err
);

int
yanet_cp_yanet_sdk_fixture_api__assign_free(
	struct cp_module *module, yanet_error **err
);
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// The families of an assign rule.
const (
	// AssignFamilyAny: the rule matches packets of either family by their
	// protocol and port only, and names no networks.
	AssignFamilyAny uint64 = C.ASSIGN_FAMILY_ANY
	// AssignFamilyIPv4: the rule matches IPv4 packets by IPv4 networks.
	AssignFamilyIPv4 uint64 = C.ASSIGN_FAMILY_IPV4
	// AssignFamilyIPv6: the rule matches IPv6 packets by IPv6 networks.
	AssignFamilyIPv6 uint64 = C.ASSIGN_FAMILY_IPV6
)

// The records of an assign build, as the C headers define them.
type (
	// Span is a run of items of an array of an assign build.
	Span = C.struct_Span
	// AssignRule is a rule of an assign module.
	AssignRule = C.struct_AssignRule
	// Net4 is an IPv4 network: an address and a mask.
	Net4 = C.struct_net4
	// Net6 is an IPv6 network: an address and a mask.
	Net6 = C.struct_net6
	// LineRange is a closed range of bytes, such as protocol numbers.
	LineRange = C.struct_classify_line_range
	// PortRange is a closed range of ports.
	PortRange = C.struct_filter_port_range
)

// NewSpan returns the run of count items of an array from offset.
func NewSpan(offset, count uint64) Span {
	return Span{offset: C.uint64_t(offset), count: C.uint64_t(count)}
}

// NewNet4 returns the network of the address under the mask.
func NewNet4(addr, mask [4]byte) Net4 {
	var record Net4
	copy(bytesOf(&record.addr), addr[:])
	copy(bytesOf(&record.mask), mask[:])
	return record
}

// NewNet6 returns the network of the address under the mask.
func NewNet6(addr, mask [16]byte) Net6 {
	var record Net6
	copy(bytesOf(&record.addr), addr[:])
	copy(bytesOf(&record.mask), mask[:])
	return record
}

// NewLineRange returns the bytes from through to.
func NewLineRange(from, to uint32) LineRange {
	return LineRange{from: C.uint32_t(from), to: C.uint32_t(to)}
}

// NewPortRange returns the ports from through to.
func NewPortRange(from, to uint16) PortRange {
	return PortRange{from: C.uint16_t(from), to: C.uint16_t(to)}
}

// NewAssignRule returns a rule of the family over the runs of the build's
// arrays, which passes or drops its packets and counts them by the counter of
// that name.
//
// It returns an error when the counter name holds a NUL or does not fit its
// field with the terminator.
func NewAssignRule(
	family uint64,
	src, dst, protos, ports Span,
	pass bool,
	counter string,
) (AssignRule, error) {
	record := AssignRule{
		family: C.uint64_t(family),
		src:    src,
		dst:    dst,
		protos: protos,
		ports:  ports,
	}
	if pass {
		record.pass = 1
	}
	if err := setName(bytesOf(&record.counter), counter); err != nil {
		return AssignRule{}, fmt.Errorf("counter name: %w", err)
	}
	return record, nil
}

// Assign is what an assign configuration is built from.
type Assign struct {
	// Rules are in order of precedence: a packet takes the first it matches.
	Rules []AssignRule
	// Nets4 and Nets6 hold the networks the rules name runs of.
	Nets4 []Net4
	Nets6 []Net6
	// Lines hold the protocols the rules name runs of.
	Lines []LineRange
	// Ports hold the destination ports the rules name runs of.
	Ports []PortRange
}

// NewAssignConfig builds an assign configuration named name from the rules in
// the memory of the agent, with a counter per rule.
func NewAssignConfig(agent *ffi.Agent, name string, assign Assign) (*ModuleConfig, error) {
	if err := refuseNUL(name); err != nil {
		return nil, fmt.Errorf("failed to build assign config: %w", err)
	}
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var pinner runtime.Pinner
	defer pinner.Unpin()
	rules, rulesLen := first(&pinner, assign.Rules)
	nets4, nets4Len := first(&pinner, assign.Nets4)
	nets6, nets6Len := first(&pinner, assign.Nets6)
	lines, linesLen := first(&pinner, assign.Lines)
	ports, portsLen := first(&pinner, assign.Ports)

	var cErr *C.yanet_error
	ptr := C.yanet_cp_yanet_sdk_fixture_api__assign_build(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		rules, rulesLen,
		nets4, nets4Len,
		nets6, nets6Len,
		lines, linesLen,
		ports, portsLen,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to build assign config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
		free: freeAssign,
	}, nil
}

func freeAssign(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
	var cErr *C.yanet_error
	rc, errno := C.yanet_cp_yanet_sdk_fixture_api__assign_free((*C.struct_cp_module)(ptr), &cErr)
	return int(rc), unsafe.Pointer(cErr), errno
}
