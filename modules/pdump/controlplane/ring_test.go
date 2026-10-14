package pdump

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

// Test_MaxMode_MatchesC verifies that the pure-Go mode bound matches the
// dataplane bitmap bound.
func Test_MaxMode_MatchesC(t *testing.T) {
	require.Equal(t, uint32(pdumppb.MaxMode), maxMode)
}

// stubSourceCapacity comfortably fits every test's small, non-wrapping
// pushes into a stubSource.
const stubSourceCapacity = 4096

// stubSource is a minimal fake ring source whose write position a test
// advances directly, into a fixed backing array, with no wraparound and
// no real ring frame behind it.
//
// The backing array is allocated once, on the first push, and never
// reassigned again: a concurrent read of it then never races with a
// later push growing or replacing it, the same way the real ring stays
// race-free through its write position alone. Its readable position
// never moves: readable marks the oldest position a record was evicted
// up to, not the newest write, and this stub never evicts. The real
// framing and wraparound rules are covered in
// objects/ring/bindings/go/cring.
type stubSource struct {
	data  []byte
	write atomic.Uint64
}

func (m *stubSource) Indices() (write, readable uint64) {
	return m.write.Load(), 0
}

func (m *stubSource) CopyRange(dst []byte, start, size uint64) {
	copy(dst, m.data[start:start+size])
}

// push appends a frame-aligned chunk at the current write position and
// publishes it.
//
// Only the test goroutine ever calls it, so the lazy allocation below
// never races with itself.
func (m *stubSource) push(chunk []byte) {
	if m.data == nil {
		m.data = make([]byte, stubSourceCapacity)
	}

	start := m.write.Load()
	if start+uint64(len(chunk)) > uint64(len(m.data)) {
		panic("stubSource: push exceeded stubSourceCapacity; this stub does not wrap")
	}
	copy(m.data[start:], chunk)
	m.write.Add(uint64(len(chunk)))
}

// signalingSource wraps a stubSource and closes a channel the moment
// its position is queried for the first time, after that query already
// read the current value.
//
// A test waits on that channel to know exactly when a tail reader has
// captured its starting position, so a write issued right after is
// guaranteed to land after that position, never folded into it.
type signalingSource struct {
	*stubSource
	called chan struct{}
	once   sync.Once
}

func newSignalingSource() *signalingSource {
	return &signalingSource{stubSource: &stubSource{}, called: make(chan struct{})}
}

func (m *signalingSource) Indices() (write, readable uint64) {
	write, readable = m.stubSource.Indices()
	m.once.Do(func() { close(m.called) })
	return write, readable
}

// waitStarted waits for a tail reader to have read this source's starting
// position.
func (m *signalingSource) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-m.called:
	case <-time.After(time.Second):
		t.Fatal("the reader never started reading its source")
	}
}

// newRecordFrame builds the ring's own 8-byte frame around the given
// metadata and payload, aligned to 4 bytes like the real writer.
func newRecordFrame(meta, data []byte, seqno uint32) []byte {
	totalLen := uint32(cring.RecordFrameSize) + uint32(len(meta)) + uint32(len(data))
	aligned := (totalLen + 3) &^ 3
	buf := make([]byte, aligned)
	binary.LittleEndian.PutUint32(buf[0:4], totalLen)
	binary.LittleEndian.PutUint32(buf[4:8], seqno)
	copy(buf[8:], meta)
	copy(buf[8+len(meta):], data)
	return buf
}

// newRecordMeta builds the 32-byte pdump metadata block
// (modules/pdump/dataplane/record.h) with a valid magic.
func newRecordMeta(packetLen, workerIdx uint32) []byte {
	buf := make([]byte, pdumpRecordHdrSize)
	binary.LittleEndian.PutUint32(buf[0:4], pdumpRecordMagic)
	binary.LittleEndian.PutUint32(buf[4:8], packetLen)
	binary.LittleEndian.PutUint32(buf[16:20], workerIdx)
	return buf
}

// Test_Ring_ParseRecordMeta_ValidRecord verifies that a well-formed record
// decodes every fixed field, including the queue bitmap's drop bit, and
// keeps the data behind the metadata block.
func Test_Ring_ParseRecordMeta_ValidRecord(t *testing.T) {
	meta := newRecordMeta(9000, 3)
	binary.LittleEndian.PutUint64(meta[8:16], 123456789)
	binary.LittleEndian.PutUint32(meta[20:24], 7)
	binary.LittleEndian.PutUint16(meta[24:26], 11)
	binary.LittleEndian.PutUint16(meta[26:28], 22)
	meta[28] = pdumppb.MaxMode // the drop bit plus every other mode bit

	payload := append(meta, []byte("hello")...)

	got, data, ok := parseRecordMeta(payload)
	require.True(t, ok)
	want := &pdumppb.RecordMeta{
		Timestamp:   123456789,
		DataSize:    uint32(len("hello")),
		PacketLen:   9000,
		WorkerIdx:   3,
		PipelineIdx: 7,
		RxDeviceId:  11,
		TxDeviceId:  22,
		Queue:       uint32(pdumppb.MaxMode),
	}
	require.Equal(t, want, got)
	require.Equal(t, []byte("hello"), data)
}

// Test_Ring_ParseRecordMeta_ShortRecord verifies that a payload shorter
// than the metadata block is dropped rather than read out of bounds.
func Test_Ring_ParseRecordMeta_ShortRecord(t *testing.T) {
	_, _, ok := parseRecordMeta(make([]byte, pdumpRecordHdrSize-1))
	require.False(t, ok)
}

// Test_Ring_ParseRecordMeta_BadMagic verifies that a metadata block whose
// magic does not match is dropped even though its length is otherwise
// valid.
func Test_Ring_ParseRecordMeta_BadMagic(t *testing.T) {
	payload := newRecordMeta(1, 0)
	payload[0] ^= 0xFF

	_, _, ok := parseRecordMeta(payload)
	require.False(t, ok)
}

// Test_Ring_RunReaders_TagsEachWorker verifies that the read session
// gives each source its own reader: a worker's record carries its own
// metadata, independently of the other worker's.
func Test_Ring_RunReaders_TagsEachWorker(t *testing.T) {
	sigs := []*signalingSource{newSignalingSource(), newSignalingSource()}
	sources := make([]cring.RecordSource, len(sigs))
	for idx, sig := range sigs {
		sources[idx] = sig
	}

	ctx, cancel := context.WithCancel(t.Context())
	recordCh := make(chan *pdumppb.Record, len(sources))
	var group errgroup.Group
	group.Go(func() error { return runReaders(ctx, sources, 64, zap.NewNop(), recordCh) })

	for _, sig := range sigs {
		sig.waitStarted(t)
	}
	for idx, sig := range sigs {
		sig.push(newRecordFrame(newRecordMeta(1, uint32(idx)), []byte{byte(idx)}, 0))
	}

	got := map[uint32][]byte{}
	for range sources {
		rec := <-recordCh
		got[rec.GetMeta().GetWorkerIdx()] = rec.GetData()
	}
	require.Equal(t, map[uint32][]byte{0: {0}, 1: {1}}, got)

	cancel()
	require.ErrorIs(t, group.Wait(), context.Canceled)
}

// Test_Ring_RunReaders_StartsAtTailSkipsHistory verifies that a session
// starts at the source's current write position: a record committed
// before the session starts is never delivered, while one committed
// after it is.
func Test_Ring_RunReaders_StartsAtTailSkipsHistory(t *testing.T) {
	sig := newSignalingSource()
	sig.push(newRecordFrame(newRecordMeta(1, 0), []byte("before"), 0))

	ctx, cancel := context.WithCancel(t.Context())
	recordCh := make(chan *pdumppb.Record, 4)
	var group errgroup.Group
	group.Go(func() error { return runReaders(ctx, []cring.RecordSource{sig}, 64, zap.NewNop(), recordCh) })

	sig.waitStarted(t)
	sig.push(newRecordFrame(newRecordMeta(1, 0), []byte("after"), 0))

	rec := <-recordCh
	require.Equal(t, []byte("after"), rec.GetData(), "a record written before the session started must never arrive")

	cancel()
	require.ErrorIs(t, group.Wait(), context.Canceled)
}

// Test_Ring_RunReaders_DropsMalformedRecordAndContinues verifies that
// malformed records are dropped without ending the session, and the
// valid record after them still arrives.
//
// Three drops must log only one first-drop warning plus one final
// count, not one line per record.
func Test_Ring_RunReaders_DropsMalformedRecordAndContinues(t *testing.T) {
	sig := newSignalingSource()

	ctx, cancel := context.WithCancel(t.Context())
	recordCh := make(chan *pdumppb.Record, 4)
	core, logs := observer.New(zapcore.WarnLevel)
	var group errgroup.Group
	group.Go(func() error { return runReaders(ctx, []cring.RecordSource{sig}, 64, zap.New(core), recordCh) })

	sig.waitStarted(t)

	badMeta := newRecordMeta(1, 0)
	badMeta[0] ^= 0xFF // corrupt the magic
	for range 3 {
		sig.push(newRecordFrame(badMeta, nil, 0))
	}
	sig.push(newRecordFrame(newRecordMeta(1, 0), []byte("ok"), 0))

	rec := <-recordCh
	require.Equal(t, []byte("ok"), rec.GetData(), "the valid record after the malformed ones must still arrive")

	cancel()
	require.ErrorIs(t, group.Wait(), context.Canceled)

	require.Equal(t, 1, logs.FilterMessage("dropped the first malformed pdump record in this session").Len(),
		"three malformed records must log the first-drop warning only once")
	require.Equal(t, 1, logs.FilterMessage("dropped malformed pdump records in this session").Len(),
		"the session must log its final drop count exactly once, when it stops")
}

// raceSourcePlainWrite is a fake ring source whose write position is a
// plain, unsynchronized field: a stand-in for memory a caller may reuse
// or free the moment a read session returns.
//
// It never actually copies any data, since no test using it ever reads
// a whole record; it exists only to let the waker poll its position.
type raceSourcePlainWrite struct {
	write uint64
}

func (m *raceSourcePlainWrite) Indices() (write, readable uint64) {
	return m.write, 0
}

func (m *raceSourcePlainWrite) CopyRange(dst []byte, start, size uint64) {}

// Test_Ring_RunReaders_StopsWakerBeforeReturning pins that the read
// session does not return until its waker has stopped touching the
// sources.
//
// The waker polls a source's indices on its own schedule, independent of
// the readers; a caller that reuses or frees that memory right after
// the session returns must never still race with it. This source's
// write position carries no synchronization of its own, so a waker
// still running when the session returned would race, under the race
// detector, with the plain write below. Repeated runs (see the gate's
// -count) make the window this pins reliably observable.
func Test_Ring_RunReaders_StopsWakerBeforeReturning(t *testing.T) {
	src := &raceSourcePlainWrite{}

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error {
		return runReaders(ctx, []cring.RecordSource{src}, 64, zap.NewNop(), make(chan *pdumppb.Record, 1))
	})

	cancel()
	require.ErrorIs(t, group.Wait(), context.Canceled)

	src.write = 1
}

// pollClock is a fake ring source that records the time of every poll.
//
// The waker reads each reader's source once per poll, so inside a
// synctest bubble the recorded times give the waker's exact intervals in
// virtual time. A poll also records whether any data had been written
// by then.
type pollClock struct {
	stubSource
	mu      sync.Mutex
	polls   []time.Time
	sawData []bool
}

func (m *pollClock) Indices() (write, readable uint64) {
	write, readable = m.stubSource.Indices()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.polls = append(m.polls, time.Now())
	m.sawData = append(m.sawData, write > 0)
	return write, readable
}

// intervals returns the time between consecutive polls, interval idx
// running from poll idx to poll idx+1, and per poll whether it saw data.
func (m *pollClock) intervals() ([]time.Duration, []bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	intervals := make([]time.Duration, 0, len(m.polls))
	for idx := 1; idx < len(m.polls); idx++ {
		intervals = append(intervals, m.polls[idx].Sub(m.polls[idx-1]))
	}
	return intervals, m.sawData
}

// startWaker runs the waker over the readers in the group and returns its
// wake-up channels.
func startWaker(ctx context.Context, group *errgroup.Group, readers []*cring.Reader) []chan struct{} {
	wakers := newWakers(len(readers))
	group.Go(func() error {
		runWaker(ctx, readers, wakers)
		return nil
	})
	return wakers
}

// Test_Ring_RunWaker_BusyReaderBacksOff verifies that a reader with
// unread data it never drains does not keep the waker polling at its
// fastest rate.
//
// A wake-up still sitting unconsumed is not a fresh one: after the first
// poll delivers it, the interval doubles on every poll up to the cap and
// stays there.
func Test_Ring_RunWaker_BusyReaderBacksOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		source := &pollClock{}
		source.push(newRecordFrame(newRecordMeta(1, 0), nil, 0))
		reader, err := cring.NewReader(0, 64, source)
		require.NoError(t, err)

		var group errgroup.Group
		startWaker(ctx, &group, []*cring.Reader{reader})
		time.Sleep(20 * wakerMaxInterval)
		cancel()
		require.NoError(t, group.Wait())

		intervals, _ := source.intervals()
		require.Greater(t, len(intervals), 10)
		want := wakerStartInterval
		for idx, interval := range intervals {
			require.Equal(t, want, interval, "interval %d", idx)
			want = min(2*want, wakerMaxInterval)
		}
	})
}

// Test_Ring_RunWaker_ResetsToStartIntervalAfterWake verifies that the
// interval returns to its starting value as soon as a poll wakes an
// idle reader, after a stretch of backing off while there was nothing
// to report.
func Test_Ring_RunWaker_ResetsToStartIntervalAfterWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		source := &pollClock{}
		reader, err := cring.NewReader(0, 64, source)
		require.NoError(t, err)

		var group errgroup.Group
		wakers := startWaker(ctx, &group, []*cring.Reader{reader})
		time.Sleep(5 * wakerMaxInterval)
		source.push(newRecordFrame(newRecordMeta(1, 0), nil, 0))
		time.Sleep(2 * wakerMaxInterval)
		cancel()
		require.NoError(t, group.Wait())

		intervals, data := source.intervals()
		first := slices.Index(data, true)
		require.Positive(t, first, "the data must arrive after some idle polls")
		require.Equal(t, wakerMaxInterval, intervals[first-1], "the idle waker must have backed off to the cap")
		require.Equal(t, wakerStartInterval, intervals[first], "a poll that wakes an idle reader resets the interval")
		require.Len(t, wakers[0], 1, "the poll that saw the record must deliver one wake-up")
	})
}

// Test_Ring_RunWaker_NotifiesOnlyAfterDataArrives verifies that the
// waker stays quiet on an empty ring, wakes the reader at the first poll
// after data is published, and stops once its context ends.
func Test_Ring_RunWaker_NotifiesOnlyAfterDataArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		source := &pollClock{}
		reader, err := cring.NewReader(0, 64, source)
		require.NoError(t, err)

		var group errgroup.Group
		wakers := startWaker(ctx, &group, []*cring.Reader{reader})
		time.Sleep(5 * wakerMaxInterval)
		require.Empty(t, wakers[0], "the waker must not notify before any data exists")

		source.push(newRecordFrame(newRecordMeta(1, 0), nil, 0))
		time.Sleep(wakerMaxInterval)
		synctest.Wait()
		require.Len(t, wakers[0], 1, "the first poll after the data arrived must notify")

		cancel()
		require.NoError(t, group.Wait())
	})
}

// retiredLease is a fake ring lease a test uses only to retire a
// binding; none of its other methods are meant to be called once that
// has happened.
type retiredLease struct{}

func (retiredLease) Handle() RingHandle { return 0 }
func (retiredLease) Release()           {}
func (retiredLease) Capacity() uint32   { return 0 }
func (retiredLease) Sources() ([]cring.RecordSource, error) {
	return nil, errors.New("a retired binding must not be read")
}

// Test_Capture_Read_RetiredBindingReportsErrRetired verifies that a
// capture whose binding was already released before its read started
// reports the retired-binding error instead of running a session on a
// binding that is already gone.
//
// This is the outcome of the race a read's own lookup leaves open: a
// capture retired between that lookup and the start of its read.
func Test_Capture_Read_RetiredBindingReportsErrRetired(t *testing.T) {
	binding := newBinding("A", retiredLease{})
	binding.Release() // retires the binding before any stream reads it

	entry := &capture{binding: binding}
	err := entry.Read(t.Context(), func(*pdumppb.Record) error { return nil })
	require.ErrorIs(t, err, errRetired)
}
