package testutils

/*
#cgo CFLAGS: -I../../../../
#cgo LDFLAGS: -Wl,--wrap=counter_registry_register
#cgo LDFLAGS: -Wl,--wrap=cp_object_registry_lookup -Wl,--wrap=counter_registry_lookup_index
#include <stdlib.h>
#include "counters.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// CounterFailure describes one semantic registration boundary and its prefix.
type CounterFailure struct {
	Name             string
	Size             uint64
	RegisteredBefore uint64
}

// RequiredCounterFailures selects every boundary for a service with two reals.
func RequiredCounterFailures() []CounterFailure {
	return []CounterFailure{
		{Name: "packets", Size: 1},
		{Name: "incoming", Size: 2},
		{Name: "filter_rejected", Size: 2, RegisteredBefore: 1},
		{Name: "ring_empty", Size: 2, RegisteredBefore: 2},
		{Name: "real_disabled", Size: 2, RegisteredBefore: 3},
		{Name: "icmp_replied", Size: 2, RegisteredBefore: 4},
		{Name: "real/0", Size: 2, RegisteredBefore: 5},
		{Name: "real/1", Size: 2, RegisteredBefore: 6},
	}
}

// CounterWitness proves the selected registry prefix reached one refusal.
type CounterWitness struct {
	Matches          uint64
	Failures         uint64
	RegisteredBefore uint64
}

// WithCounterFailure refuses one registration during a synchronous operation.
//
// Thread-local state is cleared on every exit, including panics. Untargeted
// registrations use the production implementation; this is not allocator OOM.
func WithCounterFailure(
	service string,
	failure CounterFailure,
	operation func() error,
) (witness CounterWitness, err error) {
	cService := C.CString(service)
	defer C.free(unsafe.Pointer(cService))
	cCounter := C.CString(failure.Name)
	defer C.free(unsafe.Pointer(cCounter))

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if C.l3b_test_counter_fault_begin(cService, cCounter, C.uint64_t(failure.Size)) != 0 {
		return CounterWitness{}, fmt.Errorf("counter fault already active or identity too long")
	}
	defer func() {
		observed := C.l3b_test_counter_fault_end()
		witness = CounterWitness{
			Matches:          uint64(observed.matches),
			Failures:         uint64(observed.failures),
			RegisteredBefore: uint64(observed.registered_before),
		}
	}()
	return witness, operation()
}

// AllocationWitness proves successful registration and complete arena exhaustion.
type AllocationWitness struct {
	Matches            uint64
	RegisteredCounters uint64
	ReservedBlocks     uint64
	ReservedBytes      uint64
	Exhausted          bool
}

// WithRealServerAllocationFailure exhausts an idle arena after two real counters.
//
// The operation must keep its agent alive and perform only synchronous candidate
// construction with two reals. Reservations are released before returning, even
// on panic; other users must not allocate or free from this arena concurrently.
func WithRealServerAllocationFailure(
	service string,
	operation func() error,
) (witness AllocationWitness, err error) {
	cService := C.CString(service)
	defer C.free(unsafe.Pointer(cService))
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if C.l3b_test_allocation_fault_begin(cService) != 0 {
		return AllocationWitness{}, fmt.Errorf("allocation fault already active or identity too long")
	}
	defer func() {
		observed := C.l3b_test_allocation_fault_end()
		witness = AllocationWitness{
			Matches:            uint64(observed.matches),
			RegisteredCounters: uint64(observed.registered_counters),
			ReservedBlocks:     uint64(observed.reserved_blocks),
			ReservedBytes:      uint64(observed.reserved_bytes),
			Exhausted:          observed.exhausted != 0,
		}
	}()
	return witness, operation()
}

// CounterLink identifies the module-to-service relation wired by a test.
type CounterLink struct {
	Device     string
	Pipeline   string
	Function   string
	Chain      string
	ModuleType string
	Module     string
}

// CounterKey distinguishes service counters from their module-link counters.
type CounterKey struct {
	Name string
	Link bool
}

// CounterState records the published mapping and its per-worker values.
type CounterState struct {
	Size   uint64
	Offset uint64
	Values [2]uint64
}

// PublishedCounterState is a non-owning snapshot of a settled generation.
type PublishedCounterState struct {
	Service         uintptr
	Table           uintptr
	TableReferences uint64
	ObjectStorage   uintptr
	LinkStorage     uintptr
	Counters        map[CounterKey]CounterState
}

// ReadCounterState snapshots both registries without changing their ownership.
//
// The caller must keep the worker generation alive and run no concurrent config
// updates or packets while reading its ordinary counter values.
func ReadCounterState(
	executionContext uintptr,
	service string,
	link CounterLink,
) (PublishedCounterState, error) {
	strings := []string{service, link.Device, link.Pipeline, link.Function, link.Chain, link.ModuleType, link.Module}
	arguments := make([]*C.char, len(strings))
	for idx, value := range strings {
		arguments[idx] = C.CString(value)
		defer C.free(unsafe.Pointer(arguments[idx]))
	}
	cLink := C.struct_l3b_test_counter_link{
		device:      arguments[1],
		pipeline:    arguments[2],
		function:    arguments[3],
		chain:       arguments[4],
		module_type: arguments[5],
		module:      arguments[6],
	}
	var observed C.struct_l3b_test_counter_state
	if C.l3b_test_counter_state_read(C.uintptr_t(executionContext), arguments[0], &cLink, &observed) != 0 {
		return PublishedCounterState{}, fmt.Errorf("published counters of service %q unavailable", service)
	}
	state := PublishedCounterState{
		Service:         uintptr(observed.service),
		Table:           uintptr(observed.table),
		TableReferences: uint64(observed.table_references),
		ObjectStorage:   uintptr(observed.object_storage),
		LinkStorage:     uintptr(observed.link_storage),
		Counters:        map[CounterKey]CounterState{},
	}
	for idx := range int(observed.count) {
		counter := observed.counters[idx]
		state.Counters[CounterKey{Name: C.GoString(&counter.name[0]), Link: counter.link != 0}] = CounterState{
			Size:   uint64(counter.size),
			Offset: uint64(counter.offset),
			Values: [2]uint64{uint64(counter.values[0]), uint64(counter.values[1])},
		}
	}
	return state, nil
}

// ServiceLookup binds a copied counter ID to its protected generation and object.
//
// Addresses are identity values, not handles that may be dereferenced afterward.
type ServiceLookup struct {
	Generation       uintptr
	GenerationNumber uint64
	Object           uintptr
	CounterRegistry  uintptr
	CounterID        uint64
}

// LookupWitness records real lookup hits, missing protection and provenance errors.
type LookupWitness struct {
	Lookups             []ServiceLookup
	CounterLookups      uint64
	UnprotectedObjects  uint64
	UnprotectedCounters uint64
	ProvenanceErrors    uint64
}

// WithLinkedServiceLookup observes a synchronous update without pinning its reads.
//
// The caller keeps the agent alive. Thread-local observation is disarmed on every
// exit; unrelated threads and unarmed calls use the real implementation unchanged.
func WithLinkedServiceLookup(
	agent *ffi.Agent,
	operation func() error,
) (witness LookupWitness, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if C.l3b_test_lookup_begin((*C.struct_agent)(agent.AsRawPtr())) != 0 {
		return LookupWitness{}, fmt.Errorf("lookup observer unavailable or already active")
	}
	defer func() {
		observed := C.l3b_test_lookup_end()
		witness.CounterLookups = uint64(observed.counter_lookups)
		witness.UnprotectedObjects = uint64(observed.unprotected_objects)
		witness.UnprotectedCounters = uint64(observed.unprotected_counters)
		witness.ProvenanceErrors = uint64(observed.provenance_errors)
		count := min(uint64(observed.object_lookups), uint64(C.L3B_TEST_COUNTER_CAPACITY))
		witness.Lookups = make([]ServiceLookup, count)
		for idx := range witness.Lookups {
			item := observed.lookups[idx]
			witness.Lookups[idx] = ServiceLookup{
				Generation:       uintptr(item.generation),
				GenerationNumber: uint64(item.generation_number),
				Object:           uintptr(item.object),
				CounterRegistry:  uintptr(item.counter_registry),
				CounterID:        uint64(item.counter_id),
			}
		}
	}()
	return witness, operation()
}

// ReadModuleCounterIDs copies IDs from an idle, live unpublished module candidate.
func ReadModuleCounterIDs(module ffi.ModuleConfig, count int) ([]uint64, error) {
	if count < 0 || count > int(C.L3B_TEST_COUNTER_CAPACITY) {
		return nil, fmt.Errorf("unsupported observed rule count %d", count)
	}
	ids := make([]C.uint64_t, count)
	var destination *C.uint64_t
	if count > 0 {
		destination = &ids[0]
	}
	if C.l3b_test_module_counter_ids(
		(*C.struct_cp_module)(module.AsRawPtr()),
		destination,
		C.uint32_t(count),
	) != 0 {
		return nil, fmt.Errorf("module counter array does not match %d rules", count)
	}
	result := make([]uint64, count)
	for idx := range result {
		result[idx] = uint64(ids[idx])
	}
	return result, nil
}
