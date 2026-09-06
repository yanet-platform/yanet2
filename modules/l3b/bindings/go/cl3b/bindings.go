// Package cl3b is Go binding for the l3b module
package cl3b

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/l3b/api -ll3b_cp
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//
//#include "api/agent.h"
//#include "modules/l3b/api/controlplane.h"
//#include "modules/l3b/dataplane/config.h"
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the 'l3b' module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new L3b module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.l3b_module_config_new((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to initialize module config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the underlying common module config handle.
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Free destroys the module config when it is dangling — referenced by no
// live configuration generation — and reports nil. While a live generation
// still references it the free is refused with ffi.ErrStillReferenced
// and the handle stays usable: the caller must remember it and free it
// again once the generations holding it drain. Safe to call multiple
// times: subsequent calls are no-ops reporting nil.
func (m *ModuleConfig) Free() error {
	ptr := m.asRawPtr()
	if ptr == nil {
		return nil
	}
	var cErr *C.yanet_error
	rc, errno := C.l3b_module_config_free(ptr, &cErr)
	if rc == 0 {
		m.ptr = ffi.ModuleConfig{}
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		// The refused attempt allocated an error chain; release it
		// rather than leaking one per attempt. The object is intact.
		C.yanet_error_free(cErr)
		return ffi.ErrStillReferenced
	}
	return fmt.Errorf(
		"failed to free module config: %w",
		cerrors.FromC(unsafe.Pointer(cErr)),
	)
}

// Update installs the destination filter rules into the module configuration,
// linking the virtual service object each rule names. The services must
// already be published through agent_update_objects; rules naming the same
// service share one link.
func (m *ModuleConfig) Update(rules []DestinationFilterRule) error {
	// A rule without a service name would fail the object link deep inside
	// the C update; reject it with the offending rule identified.
	for idx := range rules {
		if rules[idx].VirtualService == "" {
			return fmt.Errorf("rule %d names no virtual service", idx)
		}
	}

	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	// The C update copies each rule's service name into its module link
	// record, so the strings are freed as soon as the call returns.
	var cNames []*C.char
	defer func() {
		for _, cName := range cNames {
			C.free(unsafe.Pointer(cName))
		}
	}()

	var cRulesPtr *C.struct_l3b_destination_filter_rule
	if len(rules) > 0 {
		cRules := make([]C.struct_l3b_destination_filter_rule, len(rules))
		for idx := range rules {
			cName := C.CString(rules[idx].VirtualService)
			cNames = append(cNames, cName)
			cRules[idx] = rules[idx].cBuild(pinner, cName)
		}
		cRulesPtr = &cRules[0]
	}

	var cErr *C.yanet_error
	rc := C.l3b_module_config_update(
		m.asRawPtr(),
		cRulesPtr,
		C.uint32_t(len(rules)),
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to update module config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// DestinationFilterRule describes a destination-side classification rule.
type DestinationFilterRule struct {
	Net6s       []xnetip.BiContiguous
	Net4s       []xnetip.Contiguous[xnetip.Network4]
	ProtoRanges filter.ProtoRanges
	// Name of the linked virtual service object the rule routes to.
	VirtualService string
}

func (m *DestinationFilterRule) cBuild(
	pinner *runtime.Pinner,
	virtualService *C.char,
) C.struct_l3b_destination_filter_rule {
	c := C.struct_l3b_destination_filter_rule{}
	filter.CBuildNet6s(&c.net6s, m.Net6s, pinner)
	filter.CBuildNet4s(&c.net4s, m.Net4s, pinner)
	filter.CBuildProtoRanges(&c.proto_ranges, m.ProtoRanges, pinner)
	c.virtual_service = virtualService
	return c
}
