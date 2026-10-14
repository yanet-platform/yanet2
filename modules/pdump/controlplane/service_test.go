package pdump_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
	ring "github.com/yanet-platform/yanet2/objects/ring/controlplane"
)

// testRecordMagic mirrors PDUMP_RECORD_MAGIC (modules/pdump/dataplane/record.h).
const testRecordMagic = 0xDEADBEEF

// testRingCapacity is the smallest capacity a ring may have to be bound,
// for a test that does not care about the capacity check itself.
const testRingCapacity = pdump.MinRingCapacity

// fakeRing is an in-memory fake ring source a test writes frames into
// directly. It carries no physical wraparound, unlike a real ring,
// because every test keeps its pushes comfortably inside the capacity
// the fake was given.
//
// Its readable position never moves: readable marks the oldest position
// a record was evicted up to, not the newest write, and this fake never
// evicts. A test's capacity must stay generous enough that it never would.
type fakeRing struct {
	data  []byte
	write atomic.Uint64
	seqno atomic.Uint32
}

func newFakeRing(capacity uint32) *fakeRing {
	return &fakeRing{data: make([]byte, capacity)}
}

func (m *fakeRing) Indices() (write, readable uint64) {
	return m.write.Load(), 0
}

func (m *fakeRing) CopyRange(dst []byte, start, size uint64) {
	copy(dst, m.data[start:start+size])
}

// writeRecord writes one record, frame included, aligned to 4 bytes like
// the real writer, and publishes it.
func (m *fakeRing) writeRecord(t testing.TB, meta, payload []byte) {
	t.Helper()

	totalLen := uint32(cring.RecordFrameSize) + uint32(len(meta)) + uint32(len(payload))
	aligned := (totalLen + 3) &^ 3
	require.LessOrEqual(t, uint64(aligned), uint64(len(m.data)), "fakeRing: record larger than the fake's capacity")

	frame := make([]byte, aligned)
	seqno := m.seqno.Add(1) - 1
	binary.LittleEndian.PutUint32(frame[0:4], totalLen)
	binary.LittleEndian.PutUint32(frame[4:8], seqno)
	copy(frame[8:], meta)
	copy(frame[8+len(meta):], payload)

	start := m.write.Load()
	require.LessOrEqual(t, start+uint64(aligned), uint64(len(m.data)), "fakeRing: record would cross the end; this fake does not wrap")
	copy(m.data[start:], frame)
	m.write.Add(uint64(aligned))
}

// writePdumpRecord writes one pdump record: the 32-byte metadata block
// (modules/pdump/dataplane/record.h) for the given worker, followed by the
// packet bytes.
func (m *fakeRing) writePdumpRecord(t testing.TB, workerIdx uint32, data []byte) {
	t.Helper()

	meta := make([]byte, 32)
	binary.LittleEndian.PutUint32(meta[0:4], testRecordMagic)
	binary.LittleEndian.PutUint32(meta[4:8], uint32(len(data)))
	binary.LittleEndian.PutUint32(meta[16:20], workerIdx)
	m.writeRecord(t, meta, data)
}

// fakeRingEntry is one ring the fake ring owner knows about.
type fakeRingEntry struct {
	handle   pdump.RingHandle
	capacity uint32
	sources  []*fakeRing
	// acquireErr, when set, makes a lease request fail with this error
	// instead of granting a lease, for a test that pins how an owner
	// failure other than a gone handle gets mapped.
	acquireErr error

	mu     sync.Mutex
	leases int
}

func (m *fakeRingEntry) release() {
	m.mu.Lock()
	m.leases--
	m.mu.Unlock()
}

// fakeLease is a fake ring lease that reads the in-memory sources a
// test wrote into directly, with no dataplane ring object underneath.
type fakeLease struct {
	ringName string
	handle   pdump.RingHandle
	capacity uint32
	sources  []cring.RecordSource

	entry *fakeRingEntry
	once  sync.Once
	// opened fires once every source a stream's readers just opened has
	// had its starting position read, so a test can wait for that before
	// it writes records or changes the config: a write issued only once
	// the sources were handed back could still land before a reader's
	// own first read of the source, and so be skipped as history.
	//
	// Each time a stream fetches this lease's sources, the signal rearms
	// for that stream, so a later stream on the same lease signals again.
	opened chan struct{}
	// sourcesErr, when set, makes fetching this lease's sources fail
	// instead of opening the ones below, for a test that pins how such
	// a failure is reported.
	sourcesErr error
}

func (m *fakeLease) Handle() pdump.RingHandle { return m.handle }

func (m *fakeLease) Release() {
	m.once.Do(m.entry.release)
}

func (m *fakeLease) Capacity() uint32 { return m.capacity }

func (m *fakeLease) Sources() ([]cring.RecordSource, error) {
	if m.sourcesErr != nil {
		return nil, m.sourcesErr
	}

	var remaining atomic.Int32
	remaining.Store(int32(len(m.sources)))
	wrapped := make([]cring.RecordSource, len(m.sources))
	for idx, src := range m.sources {
		wrapped[idx] = &openSignalSource{
			RecordSource: src,
			onFirstRead: func() {
				if remaining.Add(-1) != 0 {
					return
				}
				select {
				case m.opened <- struct{}{}:
				default:
				}
			},
		}
	}
	return wrapped, nil
}

// openSignalSource wraps a ring source and fires its first-read
// callback once, the first time its position is queried, after that
// query already read the current value.
//
// It mirrors signalingSource in ring_test.go: a test waits on the
// resulting signal to know exactly when a tail reader has captured its
// starting position, so a write issued right after is guaranteed to
// land after that position, never folded into it.
type openSignalSource struct {
	cring.RecordSource
	once        sync.Once
	onFirstRead func()
}

func (m *openSignalSource) Indices() (write, readable uint64) {
	write, readable = m.RecordSource.Indices()
	m.once.Do(m.onFirstRead)
	return write, readable
}

// fakeRingOwner is an in-memory fake ring owner. Each registered ring
// has one fake source per worker that a test writes into directly:
// pdump's C library stubs DPDK and cannot link the real ring object's
// CGo harness.
type fakeRingOwner struct {
	mu         sync.Mutex
	rings      map[string]*fakeRingEntry
	nextHandle pdump.RingHandle
	// acquired records every lease handed out, in order, so a test can
	// reach the one a particular config is using.
	acquired []*fakeLease
	// afterLookupHandle, when set, runs at the end of a handle lookup,
	// after it decided its answer, so a test can simulate the registry
	// changing between a service's lookup and its subsequent acquire.
	afterLookupHandle func()
}

func newFakeRingOwner() *fakeRingOwner {
	return &fakeRingOwner{rings: map[string]*fakeRingEntry{}}
}

// register creates a named ring with the given number of fake
// per-worker sources, at the given capacity, and returns the sources
// for a test to write into.
func (m *fakeRingOwner) register(name string, capacity uint32, workerCount int) []*fakeRing {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextHandle++
	sources := make([]*fakeRing, workerCount)
	for idx := range sources {
		sources[idx] = newFakeRing(capacity)
	}
	m.rings[name] = &fakeRingEntry{
		handle:   m.nextHandle,
		capacity: capacity,
		sources:  sources,
	}
	return sources
}

func (m *fakeRingOwner) LookupHandle(name string) (pdump.RingHandle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.rings[name]
	var handle pdump.RingHandle
	if ok {
		handle = entry.handle
	}
	if m.afterLookupHandle != nil {
		m.afterLookupHandle()
	}
	return handle, ok
}

func (m *fakeRingOwner) Acquire(name string, handle pdump.RingHandle) (pdump.RingLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.rings[name]
	if !ok || entry.handle != handle {
		return nil, fmt.Errorf("ring %q handle %d no longer exists: %w", name, handle, ring.ErrHandleGone)
	}
	if entry.acquireErr != nil {
		return nil, entry.acquireErr
	}

	entry.mu.Lock()
	entry.leases++
	entry.mu.Unlock()

	sources := make([]cring.RecordSource, len(entry.sources))
	for idx, src := range entry.sources {
		sources[idx] = src
	}
	lease := &fakeLease{
		ringName: name,
		handle:   handle,
		capacity: entry.capacity,
		sources:  sources,
		entry:    entry,
		opened:   make(chan struct{}, 1),
	}
	m.acquired = append(m.acquired, lease)
	return lease, nil
}

// leaseCount returns the number of active (not yet released) leases on
// the named ring.
func (m *fakeRingOwner) leaseCount(name string) int {
	m.mu.Lock()
	entry := m.rings[name]
	m.mu.Unlock()
	if entry == nil {
		return 0
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.leases
}

// lastLease returns the most recently acquired lease for the name, the
// one a config just bound to.
func (m *fakeRingOwner) lastLease(t testing.TB, name string) *fakeLease {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, lease := range slices.Backward(m.acquired) {
		if lease.ringName == name {
			return lease
		}
	}
	t.Fatalf("no lease acquired for ring %q", name)
	return nil
}

// waitOpened waits for a stream to open the lease's sources.
func waitOpened(t *testing.T, lease *fakeLease) {
	t.Helper()

	select {
	case <-lease.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("ReadDump did not open the ring sources in time")
	}
}

// fakeModule is a published module whose state lives only in Go memory.
type fakeModule struct {
	settings pdump.Settings
	// refusals is how many frees report the module still referenced.
	refusals atomic.Int32
	freed    atomic.Bool
}

// Free refuses while refusals remain, then marks the module freed.
func (m *fakeModule) Free() error {
	if m.refusals.Add(-1) >= 0 {
		return ffi.ErrStillReferenced
	}
	m.freed.Store(true)
	return nil
}

// fakeBackend publishes fakeModules and remembers every one it built and
// every name it deleted. An update of a blocked name waits until the test
// releases it.
type fakeBackend struct {
	mu      sync.Mutex
	modules []*fakeModule
	deleted []string
	blocks  map[string]*updateBlock

	updateCalls int
	deleteErr   error
	updateErr   error
}

// updateBlock holds an update of one name inside the backend.
type updateBlock struct {
	entered chan struct{}
	release chan struct{}
}

// blockUpdate makes the next update of the name wait, and returns the
// channel closed once it entered the backend together with its release.
func (m *fakeBackend) blockUpdate(name string) (<-chan struct{}, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.blocks == nil {
		m.blocks = map[string]*updateBlock{}
	}
	block := &updateBlock{entered: make(chan struct{}), release: make(chan struct{})}
	m.blocks[name] = block

	return block.entered, sync.OnceFunc(func() { close(block.release) })
}

func (m *fakeBackend) UpdateModule(name string, settings pdump.Settings) (pdump.Module, error) {
	m.mu.Lock()
	m.updateCalls++
	block := m.blocks[name]
	delete(m.blocks, name)
	m.mu.Unlock()
	if block != nil {
		close(block.entered)
		<-block.release
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updateErr != nil {
		return nil, m.updateErr
	}

	module := &fakeModule{settings: settings}
	m.modules = append(m.modules, module)
	return module, nil
}

func (m *fakeBackend) DeleteModule(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleted = append(m.deleted, name)
	return m.deleteErr
}

// Deleted returns the names deleted so far, in order.
func (m *fakeBackend) Deleted() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]string(nil), m.deleted...)
}

// Last returns the module published most recently.
func (m *fakeBackend) Last() *fakeModule {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.modules[len(m.modules)-1]
}

// UpdateCalls returns backend entries, including updates that fail.
func (m *fakeBackend) UpdateCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.updateCalls
}

// fakeStream is a dump stream that forwards every record to a channel.
type fakeStream struct {
	grpc.ServerStream

	ctx context.Context
	out chan *pdumppb.Record
}

func (m *fakeStream) Context() context.Context {
	return m.ctx
}

func (m *fakeStream) Send(record *pdumppb.Record) error {
	select {
	case m.out <- record:
		return nil
	case <-m.ctx.Done():
		return m.ctx.Err()
	}
}

// setRing applies a ring_name-only create or update to the named config.
func setRing(t *testing.T, service *pdump.PdumpService, name, ringName string) {
	t.Helper()

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   name,
		Config: &pdumppb.Config{RingName: proto.String(ringName)},
	})
	require.NoError(t, err)
}

// newTestService returns a PdumpService over a fake backend and ring
// owner with one registered ring, "A", at the pdump minimum capacity
// with a single worker, together with the fakes and that ring's sources
// for a test to write into.
func newTestService(t *testing.T) (service *pdump.PdumpService, backend *fakeBackend, owner *fakeRingOwner, sources []*fakeRing) {
	t.Helper()

	backend = &fakeBackend{}
	owner = newFakeRingOwner()
	sources = owner.register("A", testRingCapacity, 1)
	service = pdump.NewPdumpService(backend, owner)
	return service, backend, owner, sources
}

// testStream is a read session running in its own group for the
// length of a test.
type testStream struct {
	records chan *pdumppb.Record
	// done closes once the read session has returned.
	done  chan struct{}
	group errgroup.Group
}

// openStream starts a read session for the name on the context.
//
// The test does not end before the stream does: its context ends first,
// then the cleanup waits for the read session to return.
func openStream(t *testing.T, ctx context.Context, service *pdump.PdumpService, name string) *testStream {
	t.Helper()

	stream := &testStream{
		records: make(chan *pdumppb.Record, 16),
		done:    make(chan struct{}),
	}
	stream.group.Go(func() error {
		defer close(stream.done)
		return service.ReadDump(&pdumppb.ReadDumpRequest{Name: name}, &fakeStream{ctx: ctx, out: stream.records})
	})
	t.Cleanup(func() { _ = stream.group.Wait() })
	return stream
}

// wait waits for the stream to end and returns the result of its read
// session, failing the test with the message if it does not end in time.
func (m *testStream) wait(t *testing.T, message string) error {
	t.Helper()

	select {
	case <-m.done:
		return m.group.Wait()
	case <-time.After(5 * time.Second):
		t.Fatal(message)
		return nil
	}
}

// ended reports whether the read session has already returned.
func (m *testStream) ended() bool {
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

// recvRecord waits for the next record on the channel.
func recvRecord(t *testing.T, records <-chan *pdumppb.Record) *pdumppb.Record {
	t.Helper()

	select {
	case record := <-records:
		return record
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive a record in time")
		return nil
	}
}

// Test_PdumpService_ShowConfig_UnknownName verifies that showing a name
// that was never configured reports NotFound.
func Test_PdumpService_ShowConfig_UnknownName(t *testing.T) {
	service := pdump.NewPdumpService(&fakeBackend{}, newFakeRingOwner())

	_, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_PdumpService_SetConfig_RejectsEmptyRingNameOnUpdate verifies that
// an update carrying an explicitly empty ring_name is rejected with its
// own message, distinct from the create case, and leaves the config and
// its binding untouched.
func Test_PdumpService_SetConfig_RejectsEmptyRingNameOnUpdate(t *testing.T) {
	service, _, owner, _ := newTestService(t)

	setRing(t, service, "capture", "A")
	require.Equal(t, 1, owner.leaseCount("A"))
	before, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
	require.NoError(t, err)

	_, err = service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "capture",
		Config: &pdumppb.Config{RingName: proto.String("")},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.NotContains(t, status.Convert(err).Message(), "create a config",
		"the update case must not reuse the create message")

	require.Equal(t, 1, owner.leaseCount("A"), "the rejected update must not touch the existing binding")
	after, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
	require.NoError(t, err)
	require.True(t, proto.Equal(before.GetConfig(), after.GetConfig()), "got %v", after.GetConfig())
}

// Test_PdumpService_SetConfig_CreateRefused verifies that a create
// request refused for any reason leaves nothing behind: no candidate ring
// keeps a lease, the config stays absent, and the backend is touched only
// when its own update is the reason for the refusal.
func Test_PdumpService_SetConfig_CreateRefused(t *testing.T) {
	cases := []struct {
		name   string
		config *pdumppb.Config
		setup  func(owner *fakeRingOwner, backend *fakeBackend)
		code   codes.Code
		// updateAttempted is set only for the row where the backend
		// update itself is the reason for the refusal; every other row
		// must never reach the backend.
		updateAttempted bool
	}{
		{
			name:   "missing ring_name",
			config: &pdumppb.Config{},
			code:   codes.InvalidArgument,
		},
		{
			name:   "empty ring_name",
			config: &pdumppb.Config{RingName: proto.String("")},
			code:   codes.InvalidArgument,
		},
		{
			name:   "ring_name not found",
			config: &pdumppb.Config{RingName: proto.String("missing")},
			code:   codes.NotFound,
		},
		{
			name:   "handle gone between the lookup and the acquire",
			config: &pdumppb.Config{RingName: proto.String("A")},
			setup: func(owner *fakeRingOwner, backend *fakeBackend) {
				owner.register("A", testRingCapacity, 1)
				owner.afterLookupHandle = func() {
					owner.rings["A"].handle++ // simulate a delete and recreate racing the lookup
				}
			},
			code: codes.NotFound,
		},
		{
			name:   "acquire failure other than a gone handle",
			config: &pdumppb.Config{RingName: proto.String("A")},
			setup: func(owner *fakeRingOwner, backend *fakeBackend) {
				owner.register("A", testRingCapacity, 1)
				owner.rings["A"].acquireErr = errors.New("owner is shutting down")
			},
			code: codes.Internal,
		},
		{
			name:   "ring below the minimum capacity",
			config: &pdumppb.Config{RingName: proto.String("small")},
			setup: func(owner *fakeRingOwner, backend *fakeBackend) {
				owner.register("small", testRingCapacity/2, 1)
			},
			code: codes.InvalidArgument,
		},
		{
			name:   "ring below the minimum capacity is refused even for a small snaplen",
			config: &pdumppb.Config{RingName: proto.String("smaller"), Snaplen: proto.Uint32(64)},
			setup: func(owner *fakeRingOwner, backend *fakeBackend) {
				owner.register("smaller", testRingCapacity/2, 1)
			},
			code: codes.InvalidArgument,
		},
		{
			name:   "backend update fails",
			config: &pdumppb.Config{RingName: proto.String("A")},
			setup: func(owner *fakeRingOwner, backend *fakeBackend) {
				owner.register("A", testRingCapacity, 1)
				backend.updateErr = errors.New("failed to compile filter")
			},
			code:            codes.Internal,
			updateAttempted: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{}
			owner := newFakeRingOwner()
			if tc.setup != nil {
				tc.setup(owner, backend)
			}
			service := pdump.NewPdumpService(backend, owner)

			_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{Name: "capture", Config: tc.config})
			require.Equal(t, tc.code, status.Code(err))

			wantUpdateCalls := 0
			if tc.updateAttempted {
				wantUpdateCalls = 1
			}
			require.Equal(t, wantUpdateCalls, backend.UpdateCalls())
			require.Zero(t, owner.leaseCount(tc.config.GetRingName()), "no candidate ring must keep a lease")

			_, err = service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
			require.Equal(t, codes.NotFound, status.Code(err))
		})
	}
}

// Test_PdumpService_Bind_AcceptsExactMinimumCapacity verifies that a ring
// of exactly the pdump minimum capacity is accepted even at the largest
// snaplen.
func Test_PdumpService_Bind_AcceptsExactMinimumCapacity(t *testing.T) {
	backend := &fakeBackend{}
	owner := newFakeRingOwner()
	owner.register("minimum", testRingCapacity, 1)
	service := pdump.NewPdumpService(backend, owner)

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "fits",
		Config: &pdumppb.Config{RingName: proto.String("minimum"), Snaplen: proto.Uint32(65535)},
	})
	require.NoError(t, err, "a ring of the minimum capacity must take the largest snaplen")
}

// Test_PdumpService_SharedRing_EachStreamSeesBothConfigsRecords verifies
// that two configs bound to the same ring get separate bindings, and that
// a reader of either one sees every record on the ring with its own
// cursor.
func Test_PdumpService_SharedRing_EachStreamSeesBothConfigsRecords(t *testing.T) {
	service, _, owner, sources := newTestService(t)

	setRing(t, service, "p1", "A")
	lease1 := owner.lastLease(t, "A")
	setRing(t, service, "p2", "A")
	lease2 := owner.lastLease(t, "A")
	require.Equal(t, 2, owner.leaseCount("A"))

	p1Records := openStream(t, t.Context(), service, "p1").records
	waitOpened(t, lease1)
	p2Records := openStream(t, t.Context(), service, "p2").records
	waitOpened(t, lease2)

	sources[0].writePdumpRecord(t, 0, []byte("one"))
	sources[0].writePdumpRecord(t, 0, []byte("two"))

	for _, records := range []<-chan *pdumppb.Record{p1Records, p2Records} {
		require.Equal(t, []byte("one"), recvRecord(t, records).GetData())
		require.Equal(t, []byte("two"), recvRecord(t, records).GetData())
	}
}

// Test_PdumpService_Bind_SameObjectUpdateKeepsStreamRunning verifies that
// an update which keeps the bound ring retains the current binding
// instead of taking a second lease, and that a stream already running
// keeps reading with no reset: records written before and after the
// update arrive in order with nothing lost or repeated.
func Test_PdumpService_Bind_SameObjectUpdateKeepsStreamRunning(t *testing.T) {
	service, _, owner, sources := newTestService(t)

	setRing(t, service, "p1", "A")
	lease := owner.lastLease(t, "A")

	stream := openStream(t, t.Context(), service, "p1")
	records := stream.records
	waitOpened(t, lease)

	sources[0].writePdumpRecord(t, 0, []byte("before"))
	require.Equal(t, []byte("before"), recvRecord(t, records).GetData())

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "p1",
		Config: &pdumppb.Config{Filter: proto.String("tcp")},
	})
	require.NoError(t, err)
	require.Equal(t, 1, owner.leaseCount("A"), "a same-object update must retain, not re-acquire, the lease")

	sources[0].writePdumpRecord(t, 0, []byte("after"))
	require.Equal(t, []byte("after"), recvRecord(t, records).GetData())

	require.False(t, stream.ended(), "a same-object update must not end the stream")
}

// Test_PdumpService_RingRebind_EndsOnlyThatConfigsStream verifies that
// rebinding one config to another ring ends only that config's stream,
// releases its old lease and leaves another config on the old ring
// untouched.
func Test_PdumpService_RingRebind_EndsOnlyThatConfigsStream(t *testing.T) {
	backend := &fakeBackend{}
	owner := newFakeRingOwner()
	sourcesA := owner.register("A", testRingCapacity, 1)
	sourcesB := owner.register("B", testRingCapacity, 1)
	service := pdump.NewPdumpService(backend, owner)

	setRing(t, service, "p1", "A")
	leaseA1 := owner.lastLease(t, "A")
	setRing(t, service, "p2", "A")
	leaseA2 := owner.lastLease(t, "A")
	require.Equal(t, 2, owner.leaseCount("A"))

	p1Stream := openStream(t, t.Context(), service, "p1")
	waitOpened(t, leaseA1)
	p2Records := openStream(t, t.Context(), service, "p2").records
	waitOpened(t, leaseA2)

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "p1",
		Config: &pdumppb.Config{RingName: proto.String("B")},
	})
	require.NoError(t, err)

	err = p1Stream.wait(t, "the rebind did not end P1's stream")
	require.NoError(t, err)
	require.Equal(t, 1, owner.leaseCount("A"), "P1's lease on A must be released")
	require.Equal(t, 1, owner.leaseCount("B"))

	// P2 is untouched: it still reads A.
	sourcesA[0].writePdumpRecord(t, 0, []byte("still-on-a"))
	require.Equal(t, []byte("still-on-a"), recvRecord(t, p2Records).GetData())

	// A fresh P1 stream now reads B, not A.
	p1Records2 := openStream(t, t.Context(), service, "p1").records
	waitOpened(t, owner.lastLease(t, "B"))
	sourcesB[0].writePdumpRecord(t, 0, []byte("on-b"))
	require.Equal(t, []byte("on-b"), recvRecord(t, p1Records2).GetData())
}

// Test_PdumpService_RingRebind_SameNameNewHandleEndsOldStream verifies
// that a ring deleted and recreated under the same name, which the owner
// reports as a new handle, is treated as a rebind: the old stream ends
// and a fresh one reads the new ring, never the deleted one.
func Test_PdumpService_RingRebind_SameNameNewHandleEndsOldStream(t *testing.T) {
	service, _, owner, _ := newTestService(t)

	setRing(t, service, "p1", "A")
	oldLease := owner.lastLease(t, "A")
	require.Equal(t, 1, owner.leaseCount("A"))

	stream := openStream(t, t.Context(), service, "p1")
	waitOpened(t, oldLease)

	// Simulate the ring being deleted and recreated under the same name:
	// the owner now hands out a new handle for "A".
	newSources := owner.register("A", testRingCapacity, 1)

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "p1",
		Config: &pdumppb.Config{RingName: proto.String("A")},
	})
	require.NoError(t, err)

	err = stream.wait(t, "a new handle under the same name did not end the old stream")
	require.NoError(t, err)
	require.Equal(t, 1, owner.leaseCount("A"), "the config must hold exactly one lease, on the new ring")

	newRecords := openStream(t, t.Context(), service, "p1").records
	waitOpened(t, owner.lastLease(t, "A"))
	newSources[0].writePdumpRecord(t, 0, []byte("on-new-ring"))
	require.Equal(t, []byte("on-new-ring"), recvRecord(t, newRecords).GetData())
}

// Test_PdumpService_FailedBinding_LeavesOriginalIntact verifies that a
// rebind or a same-object update refused for any reason detaches
// nobody.
//
// The config keeps its original binding and settings, only the
// candidate lease (if any) is released, and a stream already running
// keeps receiving records straight through the refusal.
func Test_PdumpService_FailedBinding_LeavesOriginalIntact(t *testing.T) {
	cases := []struct {
		name     string
		ringName string
		snaplen  uint32 // 0 means the request carries no snaplen
		setup    func(owner *fakeRingOwner, backend *fakeBackend)
		code     codes.Code
	}{
		{
			name:     "ring absent",
			ringName: "missing",
			code:     codes.NotFound,
		},
		{
			name:     "ring too small",
			ringName: "B",
			setup: func(owner *fakeRingOwner, backend *fakeBackend) {
				owner.register("B", testRingCapacity/2, 1)
			},
			code: codes.InvalidArgument,
		},
		{
			name:     "publish fails",
			ringName: "B",
			setup: func(owner *fakeRingOwner, backend *fakeBackend) {
				owner.register("B", testRingCapacity, 1)
				backend.updateErr = errors.New("compile failed")
			},
			code: codes.Internal,
		},
		{
			name:     "same-object publish fails",
			ringName: "A", // kept, not rebound: this is a same-object update
			snaplen:  20,
			setup: func(owner *fakeRingOwner, backend *fakeBackend) {
				backend.updateErr = errors.New("compile failed")
			},
			code: codes.Internal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, backend, owner, sources := newTestService(t)

			_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
				Name:   "p1",
				Config: &pdumppb.Config{RingName: proto.String("A"), Snaplen: proto.Uint32(10)},
			})
			require.NoError(t, err)
			before, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "p1"})
			require.NoError(t, err)

			lease := owner.lastLease(t, "A")
			records := openStream(t, t.Context(), service, "p1").records
			waitOpened(t, lease)

			if tc.setup != nil {
				tc.setup(owner, backend)
			}

			config := &pdumppb.Config{RingName: proto.String(tc.ringName)}
			if tc.snaplen != 0 {
				config.Snaplen = proto.Uint32(tc.snaplen)
			}
			_, err = service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{Name: "p1", Config: config})
			require.Equal(t, tc.code, status.Code(err))

			require.Equal(t, 1, owner.leaseCount("A"), "P1 must still hold its original lease")
			if tc.ringName != "A" {
				require.Zero(t, owner.leaseCount(tc.ringName), "the candidate lease must be released")
			}

			after, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "p1"})
			require.NoError(t, err)
			require.True(t, proto.Equal(before.GetConfig(), after.GetConfig()), "got %v", after.GetConfig())

			sources[0].writePdumpRecord(t, 0, []byte("still-streaming"))
			require.Equal(t, []byte("still-streaming"), recvRecord(t, records).GetData(),
				"the refusal must not disturb the stream already running")
		})
	}
}

// Test_PdumpService_DeleteConfig_ReleasesRingLeaseButKeepsRing verifies
// that deleting a config ends that config's own stream and releases its
// ring lease without deleting the ring, while another config on the same
// ring keeps streaming untouched.
func Test_PdumpService_DeleteConfig_ReleasesRingLeaseButKeepsRing(t *testing.T) {
	service, backend, owner, sources := newTestService(t)

	setRing(t, service, "p1", "A")
	lease1 := owner.lastLease(t, "A")
	setRing(t, service, "p2", "A")
	require.Equal(t, 2, owner.leaseCount("A"))
	lease2 := owner.lastLease(t, "A")

	p1Stream := openStream(t, t.Context(), service, "p1")
	waitOpened(t, lease1)
	p2Records := openStream(t, t.Context(), service, "p2").records
	waitOpened(t, lease2)

	_, err := service.DeleteConfig(t.Context(), &pdumppb.DeleteConfigRequest{Name: "p1"})
	require.NoError(t, err)

	err = p1Stream.wait(t, "the delete did not end P1's own stream")
	require.NoError(t, err)

	require.Equal(t, 1, owner.leaseCount("A"), "P1's lease must be released")
	require.Equal(t, []string{"p1"}, backend.Deleted())

	sources[0].writePdumpRecord(t, 0, []byte("still-here"))
	require.Equal(t, []byte("still-here"), recvRecord(t, p2Records).GetData())

	_, err = service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "p1"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_PdumpService_RefusedFree_ReleasesBindingExactlyOnce verifies that a
// module whose free the backend refuses several times, retried by later
// mutations, drops its capture's binding reference exactly once while the
// module free itself keeps being retried until it succeeds.
func Test_PdumpService_RefusedFree_ReleasesBindingExactlyOnce(t *testing.T) {
	service, backend, owner, _ := newTestService(t)

	setRing(t, service, "capture", "A")
	replaced := backend.Last()
	replaced.refusals.Store(2) // refuse the first two frees

	// A same-object update retires the old module but shares its binding
	// with the new entry, so the binding must not drop to zero even
	// though its module free is refused.
	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "capture",
		Config: &pdumppb.Config{Filter: proto.String("tcp")},
	})
	require.NoError(t, err)
	published := backend.Last()
	require.False(t, replaced.freed.Load(), "a refused free must leave the module allocated")
	require.Equal(t, 1, owner.leaseCount("A"), "the binding must survive its first, refused free")

	// A later mutation of another name retries the deferred free via the
	// configstore. If release-once did not hold, this retry would drop
	// the shared binding's lease a second time while "capture" still uses
	// it.
	setRing(t, service, "other", "A")
	require.False(t, replaced.freed.Load())
	require.False(t, published.freed.Load(), "the module just published must stay live")
	require.Equal(t, 2, owner.leaseCount("A"), "capture's retained lease plus other's own lease")

	setRing(t, service, "another", "A")
	require.True(t, replaced.freed.Load(), "the module free must keep being retried until it succeeds")
	require.False(t, published.freed.Load(), "retrying the replaced module's free must never touch the live one")
	require.Equal(t, 3, owner.leaseCount("A"), "the retried free must not drop the shared binding again")
}

// Test_PdumpService_SetConfig_MergesCarriedFieldsOverPublishedConfig
// verifies that an update keeps the fields it does not carry and
// publishes the merged settings.
func Test_PdumpService_SetConfig_MergesCarriedFieldsOverPublishedConfig(t *testing.T) {
	service, backend, _, _ := newTestService(t)

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name: "capture",
		Config: &pdumppb.Config{
			Filter:   proto.String("udp"),
			Mode:     proto.Uint32(2),
			Snaplen:  proto.Uint32(256),
			RingName: proto.String("A"),
		},
	})
	require.NoError(t, err)

	_, err = service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "capture",
		Config: &pdumppb.Config{Filter: proto.String("tcp")},
	})
	require.NoError(t, err)

	want := pdump.Settings{Filter: "tcp", Mode: 2, Snaplen: 256, RingName: "A"}
	require.Equal(t, want, backend.Last().settings)

	response, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
	require.NoError(t, err)
	wantConfig := &pdumppb.Config{
		Filter:   proto.String("tcp"),
		Mode:     proto.Uint32(2),
		Snaplen:  proto.Uint32(256),
		RingName: proto.String("A"),
	}
	require.True(t, proto.Equal(wantConfig, response.Config), "got %v", response.Config)
}

// Test_PdumpService_SetConfig_AppliesCarriedEmptyAndZeroValues verifies
// that a carried empty filter clears the stored one and a carried zero
// mode restores the default mode.
func Test_PdumpService_SetConfig_AppliesCarriedEmptyAndZeroValues(t *testing.T) {
	service, _, _, _ := newTestService(t)

	setRing(t, service, "defaults", "A")
	defaults, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "defaults"})
	require.NoError(t, err)

	_, err = service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "capture",
		Config: &pdumppb.Config{Filter: proto.String("udp"), Mode: proto.Uint32(2), RingName: proto.String("A")},
	})
	require.NoError(t, err)
	_, err = service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:   "capture",
		Config: &pdumppb.Config{Filter: proto.String(""), Mode: proto.Uint32(0)},
	})
	require.NoError(t, err)

	response, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
	require.NoError(t, err)
	require.Empty(t, response.GetConfig().GetFilter())
	require.NotEqual(t, uint32(2), defaults.GetConfig().GetMode())
	require.Equal(t, defaults.GetConfig().GetMode(), response.GetConfig().GetMode())
}

// Test_PdumpService_DeleteConfig_Refused verifies that a refused delete
// maps its error kind to the status code and leaves the config in place.
func Test_PdumpService_DeleteConfig_Refused(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{
			name: "referenced by a chain",
			err:  fmt.Errorf("module 'pdump:capture' not found in chain 'chain0': %w", ffi.ErrFailedPrecondition),
			code: codes.FailedPrecondition,
		},
		{
			name: "backend failure",
			err:  errors.New("dp_config_wait_for_gen timed out"),
			code: codes.Internal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{deleteErr: tc.err}
			owner := newFakeRingOwner()
			owner.register("A", testRingCapacity, 1)
			service := pdump.NewPdumpService(backend, owner)
			setRing(t, service, "capture", "A")

			_, err := service.DeleteConfig(t.Context(), &pdumppb.DeleteConfigRequest{Name: "capture"})
			require.Equal(t, tc.code, status.Code(err))

			_, err = service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
			require.NoError(t, err)
		})
	}
}

// Test_PdumpService_ReadDump_UnknownName verifies that reading a name
// that was never configured reports NotFound.
func Test_PdumpService_ReadDump_UnknownName(t *testing.T) {
	service := pdump.NewPdumpService(&fakeBackend{}, newFakeRingOwner())

	err := service.ReadDump(
		&pdumppb.ReadDumpRequest{Name: "missing"},
		&fakeStream{ctx: t.Context(), out: make(chan *pdumppb.Record, 1)},
	)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_PdumpService_ReadDump_ShutdownStopsReadersBeforeStreamsReturn
// verifies that a stream ended by a shutdown stops reading before its
// handler returns.
func Test_PdumpService_ReadDump_ShutdownStopsReadersBeforeStreamsReturn(t *testing.T) {
	service, _, owner, _ := newTestService(t)
	setRing(t, service, "capture", "A")
	lease := owner.lastLease(t, "A")

	stream := openStream(t, t.Context(), service, "capture")
	waitOpened(t, lease)

	service.Shutdown()

	err := stream.wait(t, "the shutdown did not end the stream")
	require.NoError(t, err)
}

// Test_PdumpService_ShowConfig_DoesNotWaitForPublish verifies that
// reading a config does not wait while an update of that name is inside
// the backend.
func Test_PdumpService_ShowConfig_DoesNotWaitForPublish(t *testing.T) {
	service, backend, _, _ := newTestService(t)
	setRing(t, service, "capture", "A")

	// Cleanups run last-registered first: the blocked update is released
	// before the group is waited for.
	var group errgroup.Group
	t.Cleanup(func() { _ = group.Wait() })
	entered, release := backend.blockUpdate("capture")
	t.Cleanup(release)
	group.Go(func() error {
		_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
			Name:   "capture",
			Config: &pdumppb.Config{Filter: proto.String("tcp")},
		})
		return err
	})
	<-entered

	shown := make(chan *pdumppb.ShowConfigResponse, 1)
	group.Go(func() error {
		response, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
		shown <- response
		return err
	})

	select {
	case response := <-shown:
		require.Empty(t, response.GetConfig().GetFilter(), "the blocked update must stay unpublished")
	case <-time.After(5 * time.Second):
		t.Fatal("ShowConfig waited for the blocked update")
	}

	release()
	require.NoError(t, group.Wait())
}

// Test_PdumpService_SetConfig_DoesNotSerializeNames verifies that an
// update of one name runs while an update of another name is inside the
// backend.
func Test_PdumpService_SetConfig_DoesNotSerializeNames(t *testing.T) {
	service, backend, _, _ := newTestService(t)

	// Cleanups run last-registered first: the blocked update is released
	// before the group is waited for.
	var group errgroup.Group
	t.Cleanup(func() { _ = group.Wait() })
	entered, release := backend.blockUpdate("blocked")
	t.Cleanup(release)
	group.Go(func() error {
		_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
			Name:   "blocked",
			Config: &pdumppb.Config{RingName: proto.String("A")},
		})
		return err
	})
	<-entered

	otherDone := make(chan struct{})
	group.Go(func() error {
		defer close(otherDone)
		_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
			Name:   "other",
			Config: &pdumppb.Config{RingName: proto.String("A")},
		})
		return err
	})
	select {
	case <-otherDone:
	case <-time.After(5 * time.Second):
		t.Fatal("an update of another name waited for the blocked one")
	}

	release()
	require.NoError(t, group.Wait())
}

// Test_PdumpService_ReadDump_ReportsClientCancellation verifies that a
// stream its client abandons ends with the client's cancellation.
func Test_PdumpService_ReadDump_ReportsClientCancellation(t *testing.T) {
	service, _, owner, _ := newTestService(t)
	setRing(t, service, "capture", "A")
	lease := owner.lastLease(t, "A")

	ctx, cancel := context.WithCancel(t.Context())
	stream := openStream(t, ctx, service, "capture")
	waitOpened(t, lease)

	cancel()

	err := stream.wait(t, "the cancelled client did not end the stream")
	require.ErrorIs(t, err, context.Canceled)
}

// Test_PdumpService_ReadDump_SourcesFailureIsInternal verifies that a
// stream whose ring sources cannot be reached reports Internal, not the
// Unknown code a plain, unwrapped error would produce.
func Test_PdumpService_ReadDump_SourcesFailureIsInternal(t *testing.T) {
	service, _, owner, _ := newTestService(t)
	setRing(t, service, "capture", "A")
	owner.lastLease(t, "A").sourcesErr = errors.New("ring unmapped")

	stream := openStream(t, t.Context(), service, "capture")

	err := stream.wait(t, "the stream did not report the sources failure")
	require.Equal(t, codes.Internal, status.Code(err))
}

// Test_PdumpService_ReadDump_ReaderFailureIsInternal verifies that a
// reader failure other than context cancellation reaches the client as
// Internal, instead of only being logged while the read ends as if the
// client had simply disconnected.
func Test_PdumpService_ReadDump_ReaderFailureIsInternal(t *testing.T) {
	service, _, owner, _ := newTestService(t)
	setRing(t, service, "capture", "A")
	// A capacity below the ring frame size (8 bytes) makes the reader
	// constructor inside the read session fail. The bind already passed
	// the capacity check, so the lease reports it only from now on.
	owner.lastLease(t, "A").capacity = 4

	stream := openStream(t, t.Context(), service, "capture")

	err := stream.wait(t, "the stream did not report the reader failure")
	require.Equal(t, codes.Internal, status.Code(err))
}
