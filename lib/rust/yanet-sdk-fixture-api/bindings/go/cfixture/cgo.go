//go:build sdk_rust_fixtures

// Package cfixture is the Go binding of the SDK fixture api, the control-plane
// side of the fixture, tally and fib modules that the SDK's tests run packets
// through.
package cfixture

/*
#include <stdlib.h>

#include <rte_build_config.h>

#include "api/agent.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/errors/errors.h"
#include "yanet_sdk_fixture_api.h"
#include "yanet_sdk_fixture_paths.h"

struct cp_module *
yanet_cp_yanet_sdk_fixture_api__fixture_build(
	struct agent *agent, const char *name, const char *counter,
	const char *device, const struct FixtureSpec *spec,
	const Range4 *ranges, size_t ranges_len, yanet_error **err
);

int
yanet_cp_yanet_sdk_fixture_api__fixture_free(
	struct cp_module *module, yanet_error **err
);

struct cp_module *
yanet_cp_yanet_sdk_fixture_api__tally_build(
	struct agent *agent, const char *name, uint64_t word, yanet_error **err
);

int
yanet_cp_yanet_sdk_fixture_api__tally_free(
	struct cp_module *module, yanet_error **err
);

struct cp_module *
yanet_cp_yanet_sdk_fixture_api__fib_build(
	struct agent *agent, const char *name,
	const struct FibDevice *devices, size_t devices_len,
	const struct FibRoute *routes, size_t routes_len,
	const struct FibList *lists, size_t lists_len,
	const uint64_t *indexes, size_t indexes_len,
	const Range6 *v6, size_t v6_len,
	const Range4 *v4, size_t v4_len, yanet_error **err
);

int
yanet_cp_yanet_sdk_fixture_api__fib_free(
	struct cp_module *module, yanet_error **err
);

// cgo cannot call the variadic yanet_error_add.
static void
add_cause(yanet_error **err, const char *cause) {
	yanet_error_add(err, "%s", cause);
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// PluginDir holds the fixture plugins of the configured Meson build.
const PluginDir = C.YANET_SDK_FIXTURE_PLUGIN_DIR

// PrependRoom is how many bytes a packet made from the worker pool can have
// prepended to its frame: the headroom of its buffer less the packet
// descriptor, which shares the buffer in front of the frame.
const PrependRoom = int(C.RTE_PKTMBUF_HEADROOM - C.sizeof_struct_packet)

// FibNameLen is the size of a name in a FIB record, the terminating NUL
// included.
const FibNameLen = C.FIB_NAME_LEN

// Mode selects what the fixture module does with a batch.
type Mode uint64

// The modes of the fixture module, as the header defines them.
const (
	// ModePass passes every packet.
	ModePass Mode = C.FIXTURE_MODE_PASS
	// ModeForget forgets the first packet, drops the second value unused and
	// passes the third.
	ModeForget Mode = C.FIXTURE_MODE_FORGET
	// ModeShuffle takes three packets first, disposes of them out of order
	// and passes the rest.
	ModeShuffle Mode = C.FIXTURE_MODE_SHUFFLE
	// ModeParity passes the packets whose first byte is even and drops the
	// others.
	ModeParity Mode = C.FIXTURE_MODE_PARITY
	// ModeWorker drops everything of worker 3 and everything once the round
	// time has passed 1000, and passes the rest.
	ModeWorker Mode = C.FIXTURE_MODE_WORKER
	// ModeForward redirects every packet to the output of its configured
	// device.
	ModeForward Mode = C.FIXTURE_MODE_FORWARD
	// ModeResolve passes a packet when the device resolves as an output target
	// of the generation, and drops it otherwise.
	ModeResolve Mode = C.FIXTURE_MODE_RESOLVE
	// ModeResolveInput passes a packet when the device resolves as an input
	// target of the generation, and drops it otherwise.
	ModeResolveInput Mode = C.FIXTURE_MODE_RESOLVE_INPUT
	// ModeReenter redirects a packet to the input of its configured device
	// once, marking it by the top bit of its first byte, and passes a marked
	// one.
	ModeReenter Mode = C.FIXTURE_MODE_REENTER
	// ModeEmit passes the input, makes a packet of one byte 0x5a and passes
	// it, and makes another and drops it.
	ModeEmit Mode = C.FIXTURE_MODE_EMIT
	// ModeClone passes every packet and a copy of it, whose first byte is 0xff.
	ModeClone Mode = C.FIXTURE_MODE_CLONE
	// ModeAllocPrepend passes the input, makes a packet and prepends as many
	// bytes as the argument says, filled with 0xbb, and passes it when
	// accepted or drops it when refused.
	ModeAllocPrepend Mode = C.FIXTURE_MODE_ALLOC_PREPEND
)

// Fault selects how a fixture build fails after it has allocated a block.
type Fault uint64

// The faults of a fixture build, as the header defines them.
const (
	// FaultNone: the build succeeds.
	FaultNone Fault = C.FIXTURE_FAULT_NONE
	// FaultError: the build fails with its own error.
	FaultError Fault = C.FIXTURE_FAULT_ERROR
	// FaultPanic: the build panics.
	FaultPanic Fault = C.FIXTURE_FAULT_PANIC
	// FaultPanicPayload makes the panic payload destructor panic too.
	FaultPanicPayload Fault = C.FIXTURE_FAULT_PANIC_PAYLOAD
)

// The records of the api, as the header defines them.
type (
	// FixtureSpec is what a fixture configuration is made of besides its names
	// and ranges.
	FixtureSpec = C.struct_FixtureSpec
	// Range4 is an inclusive IPv4 range of an LPM table and its value.
	Range4 = C.Range4
	// Range6 is an inclusive IPv6 range of an LPM table and its value.
	Range6 = C.Range6
	// FibDevice is a device of a FIB, by name.
	FibDevice = C.struct_FibDevice
	// FibRoute is a route of a FIB: its neighbour, the index of its device in
	// the device list and the name of its counter.
	FibRoute = C.struct_FibRoute
	// FibList is a run of entries of the route index.
	FibList = C.struct_FibList
)

// NewFixtureSpec returns the spec of a fixture configuration that behaves as
// mode says, with the argument some modes read, and fails as fault says.
func NewFixtureSpec(mode Mode, arg uint64, fault Fault) FixtureSpec {
	return FixtureSpec{
		mode:  C.uint64_t(mode),
		arg:   C.uint64_t(arg),
		fault: C.uint64_t(fault),
	}
}

// NewRange4 returns the range of addresses from through to, both included.
func NewRange4(from, to [4]byte, value uint32) Range4 {
	var record Range4
	copy(bytesOf(&record.from), from[:])
	copy(bytesOf(&record.to), to[:])
	record.value = C.uint32_t(value)
	return record
}

// NewRange6 returns the range of addresses from through to, both included.
func NewRange6(from, to [16]byte, value uint32) Range6 {
	var record Range6
	copy(bytesOf(&record.from), from[:])
	copy(bytesOf(&record.to), to[:])
	record.value = C.uint32_t(value)
	return record
}

// NewFibDevice returns the device of that name, or an error when the name
// holds a NUL or does not fit its field with the terminator.
func NewFibDevice(name string) (FibDevice, error) {
	var record FibDevice
	if err := setName(bytesOf(&record.name), name); err != nil {
		return FibDevice{}, fmt.Errorf("device name: %w", err)
	}
	return record, nil
}

// NewFibRoute returns the route through the neighbour at that address and the
// device at that index of the device list, counted by the counter of that name.
//
// It returns an error when the counter name holds a NUL or does not fit its
// field with the terminator.
func NewFibRoute(dstAddr [6]byte, device uint16, counter string) (FibRoute, error) {
	var record FibRoute
	copy(bytesOf(&record.dst_addr), dstAddr[:])
	record.device = C.uint16_t(device)
	if err := setName(bytesOf(&record.counter), counter); err != nil {
		return FibRoute{}, fmt.Errorf("counter name: %w", err)
	}
	return record, nil
}

// NewFibList returns the run of count entries of the route index from start.
func NewFibList(start, count uint64) FibList {
	return FibList{start: C.uint64_t(start), count: C.uint64_t(count)}
}

// bytesOf returns the bytes of a C array.
func bytesOf[T any](array *T) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(array)), unsafe.Sizeof(*array))
}

// setName writes the name and its terminator into the field, which starts
// zeroed.
func setName(field []byte, name string) error {
	if strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("%q holds a NUL", name)
	}
	if len(name) >= len(field) {
		return fmt.Errorf("%d bytes do not fit %d with the terminator", len(name), len(field))
	}
	copy(field, name)
	return nil
}

// ModuleConfig is a configuration built by one of the api's builders, in the
// memory of an agent.
type ModuleConfig struct {
	ptr  ffi.ModuleConfig
	free func(ptr unsafe.Pointer) (int, unsafe.Pointer, error)
}

// AsFFIModule returns the handle to publish the configuration with.
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Free destroys the configuration, or reports ffi.ErrStillReferenced while a
// live generation still holds it.
//
// A refused free keeps the handle, so that it can be retried once the
// generation is gone. Safe to call multiple times.
func (m *ModuleConfig) Free() error {
	return m.ptr.Free(m.free)
}

// first returns the address of the first of the records and their number, a
// null address and zero for none, with the records pinned.
func first[T any](pinner *runtime.Pinner, records []T) (*T, C.size_t) {
	if len(records) == 0 {
		return nil, 0
	}
	pinner.Pin(&records[0])
	return &records[0], C.size_t(len(records))
}

// NewFixtureConfig builds a fixture configuration named name in the memory of
// the agent, with the counter and the device registered under those names and
// ranges in its LPM table.
//
// A nil spec is passed on as it is, and the api refuses it.
func NewFixtureConfig(
	agent *ffi.Agent,
	name, counter, device string,
	spec *FixtureSpec,
	ranges []Range4,
) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	cCounter := C.CString(counter)
	defer C.free(unsafe.Pointer(cCounter))
	cDevice := C.CString(device)
	defer C.free(unsafe.Pointer(cDevice))

	var pinner runtime.Pinner
	defer pinner.Unpin()
	if spec != nil {
		pinner.Pin(spec)
	}
	cRanges, rangesLen := first(&pinner, ranges)

	var cErr *C.yanet_error
	ptr := C.yanet_cp_yanet_sdk_fixture_api__fixture_build(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		cCounter,
		cDevice,
		spec,
		cRanges,
		rangesLen,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to build fixture config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
		free: freeFixture,
	}, nil
}

// RefusedBuildOverCause calls the fixture build without an agent, which the
// api refuses, with a chain of one frame holding cause already in the error
// argument, as a C caller adding context to an earlier failure passes it, and
// returns the chain the call leaves there.
func RefusedBuildOverCause(cause string) error {
	cCause := C.CString(cause)
	defer C.free(unsafe.Pointer(cCause))

	var cErr *C.yanet_error
	C.add_cause(&cErr, cCause)
	C.yanet_cp_yanet_sdk_fixture_api__fixture_build(nil, nil, nil, nil, nil, nil, 0, &cErr)
	return cerrors.FromC(unsafe.Pointer(cErr))
}

func freeFixture(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
	var cErr *C.yanet_error
	rc, errno := C.yanet_cp_yanet_sdk_fixture_api__fixture_free((*C.struct_cp_module)(ptr), &cErr)
	return int(rc), unsafe.Pointer(cErr), errno
}

// NewTallyConfig builds a tally configuration named name over word in the
// memory of the agent.
func NewTallyConfig(agent *ffi.Agent, name string, word uint64) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.yanet_cp_yanet_sdk_fixture_api__tally_build(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		C.uint64_t(word),
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to build tally config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
		free: freeTally,
	}, nil
}

func freeTally(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
	var cErr *C.yanet_error
	rc, errno := C.yanet_cp_yanet_sdk_fixture_api__tally_free((*C.struct_cp_module)(ptr), &cErr)
	return int(rc), unsafe.Pointer(cErr), errno
}

// Fib is what a FIB configuration is built from.
type Fib struct {
	// Devices are registered once each and copied into the routes that use
	// them.
	Devices []FibDevice
	// Routes name their device by its index in the device list.
	Routes []FibRoute
	// Lists select runs of the route index.
	Lists []FibList
	// Indexes are the route index: the routes each list selects.
	Indexes []uint64
	// V6 and V4 map destinations to route lists.
	V6 []Range6
	V4 []Range4
}

// NewFibConfig builds a FIB configuration named name from the devices, routes,
// lists and ranges in the memory of the agent, with a counter per route.
func NewFibConfig(agent *ffi.Agent, name string, fib Fib) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var pinner runtime.Pinner
	defer pinner.Unpin()
	devices, devicesLen := first(&pinner, fib.Devices)
	routes, routesLen := first(&pinner, fib.Routes)
	lists, listsLen := first(&pinner, fib.Lists)
	indexes, indexesLen := first(&pinner, fib.Indexes)
	v6, v6Len := first(&pinner, fib.V6)
	v4, v4Len := first(&pinner, fib.V4)

	var cErr *C.yanet_error
	ptr := C.yanet_cp_yanet_sdk_fixture_api__fib_build(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		devices, devicesLen,
		routes, routesLen,
		lists, listsLen,
		(*C.uint64_t)(unsafe.Pointer(indexes)), indexesLen,
		v6, v6Len,
		v4, v4Len,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to build fib config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
		free: freeFib,
	}, nil
}

func freeFib(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
	var cErr *C.yanet_error
	rc, errno := C.yanet_cp_yanet_sdk_fixture_api__fib_free((*C.struct_cp_module)(ptr), &cErr)
	return int(rc), unsafe.Pointer(cErr), errno
}
