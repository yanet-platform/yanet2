package ring

// One lock covers each request that reads or changes the registry.
//
// The request holds the lock from start to end. This includes taking and
// releasing a lease. So a delete cannot overlap with a create of the same
// name, or with a new lease on the same handle. Without this, a consumer
// could bind by handle to a ring that the dataplane no longer has.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
	ringpb "github.com/yanet-platform/yanet2/objects/ring/controlplane/ringpb/v1"
)

// Handle identifies one ring from its create to its delete.
//
// If a ring is deleted and then created again under the same name, the new
// ring gets a new handle. A lease taken for the old handle never matches
// the new ring.
type Handle uint64

// Lease blocks the delete of one ring handle until Release.
//
// A lease comes from RingService.Acquire. Release is the only way to
// remove the block. It is safe to call more than once. It only affects the
// handle the lease was taken for.
type Lease struct {
	handle  Handle
	once    sync.Once
	release func()
}

// Handle returns the handle this lease pins.
func (m *Lease) Handle() Handle {
	return m.handle
}

// Release removes this lease's block on delete.
//
// It is safe to call more than once.
func (m *Lease) Release() {
	m.once.Do(m.release)
}

// ringEntry is one registered ring.
//
// Its fields are set once at create and never change. Only the service,
// while it holds its lock, replaces an entry or removes it from the
// registry.
type ringEntry struct {
	Handle Handle
	Name   string
	Object *cring.Object
}

// Option configures a RingService.
type Option func(*options)

type options struct {
	Log *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// WithLog sets the ring service logger.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// RingService implements the gRPC service for standalone named rings.
//
// It owns every ring it creates. It is the only code that frees a ring. It
// also gives out the leases that a consumer uses to bind to a ring by
// handle.
type RingService struct {
	ringpb.UnimplementedRingServiceServer

	mu    sync.Mutex
	agent *ffi.Agent
	rings map[string]*ringEntry
	// leases counts the active leases of each handle.
	//
	// It holds only positive counts. A missing key means zero. Each lease
	// decrements its count at most once. A delete never removes a handle
	// whose count is positive. So every decrement matches an earlier
	// increment.
	leases     map[Handle]int
	nextHandle Handle
	// deferred holds deleted entries that could not be freed yet.
	//
	// The free was refused because a live configuration generation still
	// referenced the ring. Nothing else keeps track of these entries. The
	// service tries to free them again on every delete and on an explicit
	// reclaim.
	deferred []*ringEntry
	log      *zap.Logger
}

// NewRingService creates a new RingService.
func NewRingService(agent *ffi.Agent, opts ...Option) *RingService {
	o := newOptions()
	for _, opt := range opts {
		opt(o)
	}

	return &RingService{
		agent:  agent,
		rings:  map[string]*ringEntry{},
		leases: map[Handle]int{},
		log:    o.Log,
	}
}

// CreateRing creates a new named ring and publishes it to the dataplane.
func (m *RingService) CreateRing(
	ctx context.Context,
	req *ringpb.CreateRingRequest,
) (*ringpb.CreateRingResponse, error) {
	// Validate here, not only in the gateway.
	//
	// The capacity is cut to 32 bits below. An in-process caller skips the
	// gateway, so without this check it could pass a value that does not
	// fit.
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	name := req.GetName()
	capacity := uint32(req.GetCapacity())
	publishBatch := req.PublishBatchOrDefault()

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.rings[name]; exists {
		return nil, status.Errorf(codes.AlreadyExists, "ring %q already exists", name)
	}
	if cring.Exists(m.agent, name) {
		return nil, status.Errorf(codes.AlreadyExists, "ring %q already exists", name)
	}

	object, err := cring.NewObject(m.agent, name, capacity, publishBatch)
	if err != nil {
		if errors.Is(err, cerrors.InvalidArgument) {
			return nil, status.Errorf(codes.InvalidArgument, "failed to create ring %q: %v", name, err)
		}
		m.log.Error("failed to create ring object", zap.String("ring", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create ring %q: %v", name, err)
	}

	if err := object.Publish(); err != nil {
		if err := object.Free(); err != nil {
			m.log.Error("failed to free unpublished ring", zap.String("ring", name), zap.Error(err))
		}
		m.log.Error("failed to publish ring", zap.String("ring", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to publish ring %q: %v", name, err)
	}

	m.nextHandle++
	entry := &ringEntry{Handle: m.nextHandle, Name: name, Object: object}
	m.rings[name] = entry

	m.log.Info("created ring",
		zap.String("ring", name),
		zap.Uint32("capacity", capacity),
		zap.Uint32("publish_batch", publishBatch),
	)
	return &ringpb.CreateRingResponse{}, nil
}

// ShowRing returns one named ring: its name, per-worker capacity and
// publish batch.
func (m *RingService) ShowRing(
	ctx context.Context,
	req *ringpb.ShowRingRequest,
) (*ringpb.ShowRingResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.rings[req.GetName()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "ring %q not found", req.GetName())
	}

	return &ringpb.ShowRingResponse{Ring: ringInfo(entry)}, nil
}

// ListRings returns every registered ring, sorted by name.
//
// Each ring has the same fields that ShowRing returns.
func (m *RingService) ListRings(
	ctx context.Context,
	req *ringpb.ListRingsRequest,
) (*ringpb.ListRingsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := &ringpb.ListRingsResponse{
		Rings: make([]*ringpb.RingInfo, 0, len(m.rings)),
	}
	for _, entry := range m.rings {
		response.Rings = append(response.Rings, ringInfo(entry))
	}
	slices.SortFunc(response.Rings, func(a, b *ringpb.RingInfo) int {
		return strings.Compare(a.GetName(), b.GetName())
	})
	return response, nil
}

// ringInfo builds the proto description of one registered ring.
func ringInfo(entry *ringEntry) *ringpb.RingInfo {
	return &ringpb.RingInfo{
		Name:         entry.Name,
		Capacity:     uint64(entry.Object.Capacity()),
		PublishBatch: entry.Object.PublishBatch(),
	}
}

// DeleteRing removes a named ring.
//
// The delete is refused in two cases: a lease from Acquire holds the ring,
// or a published module config links the ring by name. In both cases the
// ring stays registered and usable. The caller must release the lease or
// update the linking module, then retry.
func (m *RingService) DeleteRing(
	ctx context.Context,
	req *ringpb.DeleteRingRequest,
) (*ringpb.DeleteRingResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	name := req.GetName()

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.rings[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "ring %q not found", name)
	}

	if leaseCount := m.leases[entry.Handle]; leaseCount > 0 {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"ring %q is pinned by %d active lease(s)",
			name, leaseCount,
		)
	}

	if err := cring.DeleteObject(m.agent, name); err != nil {
		switch {
		case errors.Is(err, ffi.ErrBusy):
			m.log.Warn("ring deletion refused while linked",
				zap.String("ring", name), zap.Error(err))
			return nil, status.Errorf(
				codes.FailedPrecondition,
				"ring %q is linked by a published module config; update or delete the linking module first: %v",
				name, err,
			)
		case errors.Is(err, ffi.ErrNotFound) || !cring.Exists(m.agent, name):
			// The dataplane has nothing left to unpublish.
			//
			// Drop the entry below. Nothing else would ever remove it
			// from the registry.
			m.log.Warn("ring already absent from the dataplane; dropping it",
				zap.String("ring", name), zap.Error(err))
		default:
			// The ring is still published. Keep the entry so that a
			// retry can finish the delete.
			m.log.Error("failed to delete ring", zap.String("ring", name), zap.Error(err))
			return nil, status.Errorf(codes.Internal, "failed to delete ring %q: %v", name, err)
		}
	}

	// The delete published a generation without this ring.
	//
	// The old generation may still be live. First try again to free the
	// deferred rings. Then free this one, or defer it too.
	m.reclaimDeferred()
	m.freeOrDefer(entry)
	delete(m.rings, name)
	delete(m.leases, entry.Handle)

	m.log.Info("deleted ring", zap.String("ring", name))
	return &ringpb.DeleteRingResponse{}, nil
}

// LookupHandle returns the handle registered under a name.
//
// A consumer uses it to turn a configured ring name into the handle it
// binds through.
func (m *RingService) LookupHandle(name string) (Handle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.rings[name]
	if !ok {
		return 0, false
	}
	return entry.Handle, true
}

// Acquire blocks the delete of the named ring and returns a lease.
//
// It succeeds only if the name still maps to the given handle. Acquire
// fails once that ring is deleted, even if a new ring exists under the
// same name. The new ring got its own handle when it was created.
// Releasing the lease removes the block.
func (m *RingService) Acquire(name string, handle Handle) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.rings[name]; !ok || entry.Handle != handle {
		return nil, fmt.Errorf("ring %q handle %d no longer exists", name, handle)
	}

	m.leases[handle]++
	return &Lease{
		handle: handle,
		release: func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.leases[handle] <= 1 {
				delete(m.leases, handle)
			} else {
				m.leases[handle]--
			}
		},
	}, nil
}

// freeOrDefer frees the ring of an entry that is no longer published.
//
// If a live generation still references the ring, the free is refused and
// the entry is deferred. Any other failure is logged, and the memory is
// leaked so that it is never freed twice. The caller holds the service
// lock.
func (m *RingService) freeOrDefer(entry *ringEntry) {
	if err := entry.Object.Free(); err != nil {
		if errors.Is(err, ffi.ErrStillReferenced) {
			m.deferred = append(m.deferred, entry)
			return
		}
		m.log.Error("failed to free ring", zap.String("ring", entry.Name), zap.Error(err))
	}
}

// ReclaimDeferred tries again to free every deferred ring.
//
// It keeps only the rings that a live generation still references. A ring
// whose free fails for another reason is logged and dropped. The service
// runs it on every delete. Other code may call it at any time.
func (m *RingService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without taking the lock.
//
// The caller holds the service lock.
func (m *RingService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, entry := range m.deferred {
		if err := entry.Object.Free(); err != nil {
			if errors.Is(err, ffi.ErrStillReferenced) {
				kept = append(kept, entry)
			} else {
				m.log.Error("failed to free deferred ring", zap.String("ring", entry.Name), zap.Error(err))
			}
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
