package probe

/*
#cgo CFLAGS: -I../../../../../ -I../../../../../lib
#cgo CFLAGS: -I../../../../../modules/l3b/dataplane
#cgo CFLAGS: -D_GNU_SOURCE
#cgo amd64 CFLAGS: -march=haswell
#include <stdlib.h>
#include "probe.h"
*/
import "C"

import (
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Masks bypass typed network validation at the actual C entrypoints.
//
// Each family starts with one zero-address network. Additional masks append
// networks within the selected rule; omitted masks remain wildcards. The
// default is one rule. Selecting the second rule prepends a valid wildcard
// rule; destination protocols remain active only on the first rule.
type Masks struct {
	IPv4           [4]byte
	IPv6           [16]byte
	AdditionalIPv4 [][4]byte
	AdditionalIPv6 [][16]byte
	InSecondRule   bool
}

// rawNetworks pins one network per family plus any appended raw masks.
func rawNetworks(masks Masks, pinner *runtime.Pinner) (C.struct_filter_net4s, C.struct_filter_net6s) {
	networks4 := make([]C.struct_net4, 1+len(masks.AdditionalIPv4))
	for idx := range networks4 {
		mask := masks.IPv4
		if idx > 0 {
			mask = masks.AdditionalIPv4[idx-1]
		}
		network := &networks4[idx]
		for idx, value := range mask {
			network.mask[idx] = C.uint8_t(value)
		}
	}
	networks6 := make([]C.struct_net6, 1+len(masks.AdditionalIPv6))
	for idx := range networks6 {
		mask := masks.IPv6
		if idx > 0 {
			mask = masks.AdditionalIPv6[idx-1]
		}
		network := &networks6[idx]
		for idx, value := range mask {
			network.mask[idx] = C.uint8_t(value)
		}
	}
	pinner.Pin(&networks4[0])
	pinner.Pin(&networks6[0])
	return C.struct_filter_net4s{items: &networks4[0], count: C.uint32_t(len(networks4))},
		C.struct_filter_net6s{items: &networks6[0], count: C.uint32_t(len(networks6))}
}

// Witness records decoder progress and the actual injected allocation count.
type Witness struct {
	IPv4Decoded bool
	IPv6Entered bool
	Faults      int
}

func witness(value C.struct_l3b_probe_witness) Witness {
	return Witness{value.ip4_decode_succeeded != 0, value.ip6_decode_entered != 0, int(value.injected_allocations)}
}

// NewTable owns a real session table independent of service construction.
func NewTable(agent *ffi.Agent, name string) (ffi.ObjectConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var cError *C.yanet_error
	object := C.l3b_probe_table_create((*C.struct_agent)(agent.AsRawPtr()), cName, &cError)
	if object == nil {
		return ffi.ObjectConfig{}, cerrors.FromC(unsafe.Pointer(cError))
	}
	return ffi.NewObjectConfig(unsafe.Pointer(object)), nil
}

// FreeTable releases only the caller-owned table.
func FreeTable(table *ffi.ObjectConfig) error {
	return table.Free(func(pointer unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cError *C.yanet_error
		result, errno := C.l3b_probe_table_free((*C.struct_cp_object)(pointer), &cError)
		return int(result), unsafe.Pointer(cError), errno
	})
}

// SeedTable installs a live sentinel whose backend and deadline must survive.
func SeedTable(table ffi.ObjectConfig) bool {
	return C.l3b_probe_table_seed((*C.struct_cp_object)(table.AsRawPtr())) == 0
}

// HasSentinel checks the live flow without altering its deadline or backend.
func HasSentinel(table ffi.ObjectConfig) bool {
	return C.l3b_probe_table_has_sentinel((*C.struct_cp_object)(table.AsRawPtr())) != 0
}

// NewService borrows the table and pins raw descriptors for one synchronous call.
func NewService(agent *ffi.Agent, table ffi.ObjectConfig, name string, masks Masks, failIPv6 bool) (ffi.ObjectConfig, Witness, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var pinner runtime.Pinner
	defer pinner.Unpin()
	networks4, networks6 := rawNetworks(masks, &pinner)
	ports := C.struct_filter_port_range{from: 1, to: 65535}
	rules := []C.struct_l3b_source_filter_rule{{
		net4s:       networks4,
		net6s:       networks6,
		port_ranges: C.struct_filter_port_ranges{items: &ports, count: 1},
	}}
	if masks.InSecondRule {
		wildcardNetworks4, wildcardNetworks6 := rawNetworks(Masks{}, &pinner)
		rules = append(rules, rules[0])
		rules[0].net4s = wildcardNetworks4
		rules[0].net6s = wildcardNetworks6
	}
	service := C.struct_l3b_virtual_service{source_filter_rules: &rules[0], source_filter_rule_count: C.uint32_t(len(rules)), ring_capacity: 4}
	config := C.struct_l3b_virtual_service_create_config{
		agent: (*C.struct_agent)(agent.AsRawPtr()), name: cName,
		session_table: (*C.struct_cp_object)(table.AsRawPtr()), virtual_service: &service,
	}
	pinner.Pin(&ports)
	pinner.Pin(&rules[0])
	pinner.Pin(&service)
	pinner.Pin(&config)
	var cWitness C.struct_l3b_probe_witness
	var cError *C.yanet_error
	pinner.Pin(&cWitness)
	pinner.Pin(&cError)
	var fail C.int
	if failIPv6 {
		fail = 1
	}
	object := C.l3b_probe_service_create(&config, fail, &cWitness, &cError)
	if object == nil {
		return ffi.ObjectConfig{}, witness(cWitness), cerrors.FromC(unsafe.Pointer(cError))
	}
	return ffi.NewObjectConfig(unsafe.Pointer(object)), witness(cWitness), nil
}

// FreeService never releases its borrowed session table.
func FreeService(service *ffi.ObjectConfig) error {
	return service.Free(func(pointer unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cError *C.yanet_error
		result, errno := C.l3b_probe_service_free((*C.struct_cp_object)(pointer), &cError)
		return int(result), unsafe.Pointer(cError), errno
	})
}

// NewModule owns a fresh unpublished candidate for a single update.
func NewModule(agent *ffi.Agent, name string) (ffi.ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var cError *C.yanet_error
	module := C.l3b_probe_module_new((*C.struct_agent)(agent.AsRawPtr()), cName, &cError)
	if module == nil {
		return ffi.ModuleConfig{}, cerrors.FromC(unsafe.Pointer(cError))
	}
	return ffi.NewModuleConfig(unsafe.Pointer(module)), nil
}

// UpdateModule links a published service using pinned synchronous descriptors.
func UpdateModule(module ffi.ModuleConfig, serviceName string, masks Masks, failIPv6 bool) (Witness, error) {
	cName := C.CString(serviceName)
	defer C.free(unsafe.Pointer(cName))
	var pinner runtime.Pinner
	defer pinner.Unpin()
	networks4, networks6 := rawNetworks(masks, &pinner)
	protocols := C.struct_filter_proto_range{from: 0x0600, to: 0x06ff}
	rules := []C.struct_l3b_destination_filter_rule{{
		net4s:           networks4,
		net6s:           networks6,
		proto_ranges:    C.struct_filter_proto_ranges{items: &protocols, count: 1},
		virtual_service: cName,
	}}
	if masks.InSecondRule {
		wildcardNetworks4, wildcardNetworks6 := rawNetworks(Masks{}, &pinner)
		rules = append(rules, rules[0])
		rules[0].net4s = wildcardNetworks4
		rules[0].net6s = wildcardNetworks6
		rules[1].proto_ranges = C.struct_filter_proto_ranges{}
	}
	pinner.Pin(&protocols)
	pinner.Pin(&rules[0])
	var cWitness C.struct_l3b_probe_witness
	var cError *C.yanet_error
	pinner.Pin(&cWitness)
	pinner.Pin(&cError)
	var fail C.int
	if failIPv6 {
		fail = 1
	}
	result := C.l3b_probe_module_update((*C.struct_cp_module)(module.AsRawPtr()), &rules[0], C.uint32_t(len(rules)), fail, &cWitness, &cError)
	if result != 0 {
		return witness(cWitness), cerrors.FromC(unsafe.Pointer(cError))
	}
	return witness(cWitness), nil
}

// FreeModule also releases links allocated before a failed update.
func FreeModule(module *ffi.ModuleConfig) error {
	return module.Free(func(pointer unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cError *C.yanet_error
		result, errno := C.l3b_probe_module_free((*C.struct_cp_module)(pointer), &cError)
		return int(result), unsafe.Pointer(cError), errno
	})
}
