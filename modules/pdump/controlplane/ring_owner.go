package pdump

import (
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
	ring "github.com/yanet-platform/yanet2/objects/ring/controlplane"
)

// RingHandle identifies one ring from its create to its delete.
type RingHandle = ring.Handle

// RingLease is what a capture binding needs from an acquired ring.
//
// It must be enough to tell whether two leases pin the same ring, to
// check a ring meets the pdump minimum capacity on bind and to bound a
// reader's records against that same capacity, to read the ring's
// sources and to let the ring go again. A production lease pins a ring
// hosted in this agent; a test fakes one directly, with no dataplane
// ring object underneath.
type RingLease interface {
	// Handle returns the handle this lease pins.
	//
	// A binding compares handles, not names, to tell a same-object update
	// from a rebind: a ring deleted and recreated under the same name
	// gets a new handle.
	Handle() RingHandle
	// Release removes this lease's block on the ring's delete.
	//
	// It is safe to call more than once.
	Release()
	// Capacity returns the size of each worker's data area in bytes.
	//
	// It is the bound a reader checks a record's length against to tell
	// a real frame from corruption.
	Capacity() uint32
	// Sources returns a record source per worker's ring, indexed by
	// worker, for a reader to read from.
	Sources() ([]cring.RecordSource, error)
}

// RingOwner is where the pdump service looks up and pins its rings.
//
// Production resolves rings in the ring service hosted in this agent. A
// test fakes it with in-memory record sources, since pdump's C library
// stubs DPDK and cannot link the real ring object's CGo harness.
type RingOwner interface {
	// LookupHandle returns the handle registered under a name.
	LookupHandle(name string) (RingHandle, bool)
	// Acquire blocks the delete of the named ring and returns a lease.
	//
	// It succeeds only if the name still maps to the given handle.
	Acquire(name string, handle RingHandle) (RingLease, error)
}

// ringOwner resolves rings in the ring service this agent hosts.
type ringOwner struct {
	service *ring.RingService
}

// newRingOwner wraps the ring service this agent hosts.
func newRingOwner(service *ring.RingService) RingOwner {
	return &ringOwner{service: service}
}

func (m *ringOwner) LookupHandle(name string) (RingHandle, bool) {
	return m.service.LookupHandle(name)
}

func (m *ringOwner) Acquire(name string, handle RingHandle) (RingLease, error) {
	lease, err := m.service.Acquire(name, handle)
	if err != nil {
		return nil, err
	}
	return &ringLease{lease: lease}, nil
}

// ringLease reads a pinned ring through its dataplane object.
type ringLease struct {
	lease *ring.Lease
}

func (m *ringLease) Handle() RingHandle {
	return m.lease.Handle()
}

func (m *ringLease) Release() {
	m.lease.Release()
}

func (m *ringLease) Capacity() uint32 {
	return m.lease.Object().Capacity()
}

func (m *ringLease) Sources() ([]cring.RecordSource, error) {
	return m.lease.Object().Sources()
}
