//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// fibType is the dataplane module a FIB configuration configures.
const fibType = "fib"

// fibSource is the byte of the source address every route of a FIB
// writes.
const fibSource = 0xee

// Fib is what a FIB configuration is built from.
type Fib struct {
	// Devices are linked once each and copied into the routes that use
	// them.
	Devices []string
	// Routes name their device by its index in Devices.
	Routes []FibRoute
	// Lists select runs of the route index.
	Lists []RouteList
	// Indexes are the route index: the routes each list selects.
	Indexes []uint64
	// V6 and V4 map destinations to route lists, inserted in order.
	V6 []Range6
	V4 []Range4
}

// FibRoute is a route of a FIB: its neighbour, the index of its device in
// the device list and the name of its counter.
type FibRoute struct {
	DstAddr [6]byte
	Device  int
	Counter string
}

// NewFibConfig builds a FIB configuration named name in the agent's
// memory: the devices, the routes with a counter each and the source
// address 0xee each, the lists and the route index, then the prefix trees
// of IPv6 and IPv4.
//
// A name its field cannot hold, a route naming a device past the list and
// a range that ends before it starts are refused before anything is
// allocated. A failure frees what the build took; an exhausted agent
// fails with the allocator's out-of-memory error.
func NewFibConfig(owner *agent.Agent, name string, fib Fib) (*layout.CpModule, error) {
	for idx, route := range fib.Routes {
		if route.Device < 0 || route.Device >= len(fib.Devices) {
			return nil, fmt.Errorf(
				"route %d of fib module %q names device %d of %d: %w",
				idx, name, route.Device, len(fib.Devices), ErrInvalidArgument,
			)
		}
	}
	if err := checkRanges(fib.V6); err != nil {
		return nil, fmt.Errorf("IPv6 ranges of fib module %q: %w", name, err)
	}
	if err := checkRanges(fib.V4); err != nil {
		return nil, fmt.Errorf("IPv4 ranges of fib module %q: %w", name, err)
	}
	build := func(config *moduleBlock[RouteFib]) error {
		return buildFib(config, fib)
	}
	return newModule(owner, fibType, name, build, destroyFib)
}

// FreeFibConfig frees a FIB configuration no generation holds.
//
// One a generation holds is refused as still referenced, and nothing is
// freed.
func FreeFibConfig(module *layout.CpModule) error {
	return freeModule(module, destroyFib)
}

// buildFib builds the body in its order: the devices, the routes, the
// lists, the route index, then the prefix trees.
func buildFib(config *moduleBlock[RouteFib], fib Fib) error {
	module := &config.Header
	context := &module.Entity.MemoryContext
	body := &config.Body
	devices := make([]uint64, 0, len(fib.Devices))
	for _, name := range fib.Devices {
		index, err := cpconfig.LinkDevice(module, name)
		if err != nil {
			return fmt.Errorf("device %q: %w", name, err)
		}
		devices = append(devices, index)
	}
	routes, err := newSlice(context, &body.Routes, uint64(len(fib.Routes)))
	if err != nil {
		return fmt.Errorf("routes: %w", err)
	}
	for idx, route := range fib.Routes {
		id, err := cpconfig.RegisterCounter(&module.CounterRegistry, route.Counter, countValues)
		if err != nil {
			return fmt.Errorf("counter of route %d: %w", idx, err)
		}
		routes[idx] = Route{
			DstAddr: route.DstAddr,
			SrcAddr: [6]byte{fibSource, fibSource, fibSource, fibSource, fibSource, fibSource},
			Device:  Device{Index: devices[route.Device]},
			Counter: Counter{ID: id},
		}
	}
	lists, err := newSlice(context, &body.RouteLists, uint64(len(fib.Lists)))
	if err != nil {
		return fmt.Errorf("route lists: %w", err)
	}
	copy(lists, fib.Lists)
	indexes, err := newSlice(context, &body.RouteIndexes, uint64(len(fib.Indexes)))
	if err != nil {
		return fmt.Errorf("route indexes: %w", err)
	}
	copy(indexes, fib.Indexes)
	if err := buildLpm(&body.LpmV6, context, fib.V6); err != nil {
		return fmt.Errorf("IPv6 ranges: %w", err)
	}
	if err := buildLpm(&body.LpmV4, context, fib.V4); err != nil {
		return fmt.Errorf("IPv4 ranges: %w", err)
	}
	return nil
}

// destroyFib frees what the body holds, field by field, then the header
// and the block.
func destroyFib(config *moduleBlock[RouteFib]) {
	context := &config.Header.Entity.MemoryContext
	body := &config.Body
	freeLpm(&body.LpmV6)
	freeLpm(&body.LpmV4)
	freeSlice(context, &body.Routes)
	freeSlice(context, &body.RouteLists)
	freeSlice(context, &body.RouteIndexes)
	finiModule(config)
}
