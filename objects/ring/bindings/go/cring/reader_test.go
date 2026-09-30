package cring_test

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
	"github.com/yanet-platform/yanet2/objects/ring/tests/ringtest"
)

// repeatedFrameSizePayload returns the given number of bytes filled with the
// record frame size as little-endian words.
//
// An evicting write with this payload leaves only such words in the ring.
// If a bug lets stale or torn bytes into a result, they parse as a valid
// empty record. They do not hit the length range check by chance. So a test
// can tell a missed recheck from a caught corruption.
func repeatedFrameSizePayload(n int) []byte {
	payload := make([]byte, n)
	for idx := 0; idx+4 <= n; idx += 4 {
		binary.LittleEndian.PutUint32(payload[idx:idx+4], cring.RecordFrameSize)
	}
	return payload
}

// newRingObject creates and publishes a ring object with the default
// publish batch, freeing it at test end.
func newRingObject(t testing.TB, agent *ffi.Agent, name string, capacity uint32) *cring.Object {
	t.Helper()

	object, err := cring.NewObject(agent, name, capacity, cring.DefaultPublishBatch)
	require.NoError(t, err)
	t.Cleanup(func() { _ = object.Free() })

	require.NoError(t, object.Publish())
	return object
}

// source returns the real record source of one worker's ring.
func source(t testing.TB, object *cring.Object, workerIdx uint16) cring.RecordSource {
	t.Helper()

	sources, err := object.Sources()
	require.NoError(t, err)
	require.Less(t, int(workerIdx), len(sources))
	return sources[workerIdx]
}

// openReader opens a fresh reader over one worker's ring.
func openReader(t testing.TB, object *cring.Object, workerIdx uint16) *cring.Reader {
	t.Helper()

	readers, err := object.OpenReaders()
	require.NoError(t, err)
	require.Less(t, int(workerIdx), len(readers))
	return readers[workerIdx]
}

// newWriter returns the raw C writer of one worker.
//
// A test uses it to write records the same way the dataplane does.
func newWriter(t testing.TB, object *cring.Object, workerIdx uint16) *ringtest.Writer {
	t.Helper()

	writer, err := ringtest.NewWriter(object.AsRawPtr(), workerIdx)
	require.NoError(t, err)
	return writer
}

// Test_Reader_Read_RoundTripAcrossPhysicalWrap verifies that a record that
// crosses the ring's physical end is read back unchanged.
func Test_Reader_Read_RoundTripAcrossPhysicalWrap(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "wrap", 32)
	writer := newWriter(t, object, 0)

	// The frame ends just before the physical end, and the payload crosses
	// it: bytes [28,32) then [0,4). The C wrap test uses the same layout.
	writer.SetIndices(20, 20)

	payload := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	seqno, err := writer.WriteRecord(payload)
	require.NoError(t, err)
	require.Equal(t, uint32(0), seqno)

	reader := openReader(t, object, 0)

	records := reader.Read(1024)
	require.Len(t, records, 1)
	require.Equal(t, uint16(0), records[0].Worker)
	require.Equal(t, uint32(0), records[0].Seqno)
	require.Equal(t, payload, records[0].Bytes)
}

// Test_Reader_Read_RoundTripAcrossWorkers verifies that the object keeps its
// capacity and that each worker gets its own reader.
//
// Each reader sees only the records of its own worker.
func Test_Reader_Read_RoundTripAcrossWorkers(t *testing.T) {
	const workerCount = 3

	agent := newTestAgent(t, workerCount)
	object := newRingObject(t, agent, "workers", 64)
	require.Equal(t, uint32(64), object.Capacity())

	for workerIdx := range workerCount {
		writer := newWriter(t, object, uint16(workerIdx))
		_, err := writer.WriteRecord([]byte{byte(workerIdx), byte(workerIdx)})
		require.NoError(t, err)
	}

	readers, err := object.OpenReaders()
	require.NoError(t, err)
	require.Len(t, readers, workerCount)

	for workerIdx, reader := range readers {
		records := reader.Read(1024)
		require.Len(t, records, 1)
		require.Equal(t, uint16(workerIdx), records[0].Worker)
		require.Equal(t, []byte{byte(workerIdx), byte(workerIdx)}, records[0].Bytes)
	}
}

// Test_Reader_Read_CorruptFrameResyncsToWriteBoundary verifies that after a
// corrupt frame the reader resumes at the write position it loaded.
//
// So a later read never parses payload bytes as a frame.
func Test_Reader_Read_CorruptFrameResyncsToWriteBoundary(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "corrupt-frame", 64)
	writer := newWriter(t, object, 0)

	// The first payload is two frame-size words.
	//
	// A reader at a wrong offset would parse them as valid empty records
	// and not fail the length check. The record starts the empty ring, at
	// offset 0.
	_, err := writer.WriteRecord(repeatedFrameSizePayload(8))
	require.NoError(t, err)
	writer.CorruptTotalLen(0, 0xffffffff)

	_, err = writer.WriteRecord([]byte("BBBBBBBB"))
	require.NoError(t, err)

	reader := openReader(t, object, 0)

	// A small read stops inside the first payload, far before the write
	// position. So the read position could end in the middle of a record.
	require.Empty(t, reader.Read(12))

	_, err = writer.WriteRecord([]byte("CCCCCCCC"))
	require.NoError(t, err)

	records := reader.Read(1024)
	require.Len(t, records, 1)
	require.Equal(t, []byte("CCCCCCCC"), records[0].Bytes)
}

// Test_Reader_Read_CorruptFrameReturnsEarlierRecords verifies that a read
// returns the records before a corrupt frame and drops the rest.
//
// The corrupt length is either above the capacity or below the frame size.
func Test_Reader_Read_CorruptFrameReturnsEarlierRecords(t *testing.T) {
	for _, totalLen := range []uint32{0xffffffff, 4, 0} {
		t.Run(fmt.Sprintf("length=%d", totalLen), func(t *testing.T) {
			agent := newTestAgent(t, 1)
			object := newRingObject(t, agent, "corrupt-after-good", 128)
			writer := newWriter(t, object, 0)

			// "good-one" makes a 16-byte record, so the next one starts
			// at offset 16.
			_, err := writer.WriteRecord([]byte("good-one"))
			require.NoError(t, err)
			_, err = writer.WriteRecord([]byte("bad-frame"))
			require.NoError(t, err)
			writer.CorruptTotalLen(16, totalLen)
			_, err = writer.WriteRecord([]byte("dropped"))
			require.NoError(t, err)

			reader := openReader(t, object, 0)

			records := reader.Read(1024)
			require.Len(t, records, 1)
			require.Equal(t, []byte("good-one"), records[0].Bytes)
		})
	}
}

// Test_Reader_Read_TwoIndependentReadersSeeSameStream verifies that two
// readers of one worker each see every record.
//
// Each reader has its own read position.
func Test_Reader_Read_TwoIndependentReadersSeeSameStream(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "two-readers", 128)
	writer := newWriter(t, object, 0)

	_, err := writer.WriteRecord([]byte("record-a"))
	require.NoError(t, err)

	readerOne := openReader(t, object, 0)
	readerTwo := openReader(t, object, 0)

	recordsOne := readerOne.Read(1024)
	require.Len(t, recordsOne, 1)
	require.Equal(t, []byte("record-a"), recordsOne[0].Bytes)

	// The second reader has its own position, so it still sees the record
	// the first reader already read.
	recordsTwo := readerTwo.Read(1024)
	require.Len(t, recordsTwo, 1)
	require.Equal(t, []byte("record-a"), recordsTwo[0].Bytes)

	_, err = writer.WriteRecord([]byte("record-b"))
	require.NoError(t, err)

	recordsOne = readerOne.Read(1024)
	require.Len(t, recordsOne, 1)
	require.Equal(t, []byte("record-b"), recordsOne[0].Bytes)

	recordsTwo = readerTwo.Read(1024)
	require.Len(t, recordsTwo, 1)
	require.Equal(t, []byte("record-b"), recordsTwo[0].Bytes)
}

// Test_Reader_Read_SeesNothingUntilPublish verifies that readers do not see
// committed records until the writer publishes them.
//
// It also checks that sequence numbers stay consecutive across
// publications.
func Test_Reader_Read_SeesNothingUntilPublish(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "publish", 256)
	writer := newWriter(t, object, 0)
	reader := openReader(t, object, 0)

	first, err := writer.CommitRecord([]byte("one"))
	require.NoError(t, err)
	_, err = writer.CommitRecord([]byte("two"))
	require.NoError(t, err)
	require.False(t, reader.HasMore(), "a committed batch must stay invisible")
	require.Empty(t, reader.Read(1024))

	writer.Publish()
	records := reader.Read(1024)
	require.Len(t, records, 2)
	require.Equal(t, first, records[0].Seqno)
	require.Equal(t, first+1, records[1].Seqno)

	_, err = writer.CommitRecord([]byte("three"))
	require.NoError(t, err)
	require.Empty(t, reader.Read(1024))
	writer.Publish()
	records = reader.Read(1024)
	require.Len(t, records, 1)
	require.Equal(t, first+2, records[0].Seqno, "sequence numbers must continue across publications")
	require.Equal(t, []byte("three"), records[0].Bytes)
}

// hookedSource wraps a real RecordSource and runs test actions at chosen
// points of a read.
//
// The position hook runs on every load with its 1-based number, counted
// across all reads, so a test can target the recheck of any read. The copy
// hook runs before the first copy only.
type hookedSource struct {
	real      cring.RecordSource
	onIndices func(call int)
	onCopy    func()
	calls     int
	copied    bool
}

func (m *hookedSource) Indices() (uint64, uint64) {
	m.calls++
	if m.onIndices != nil {
		m.onIndices(m.calls)
	}
	return m.real.Indices()
}

func (m *hookedSource) CopyRange(dst []byte, start, size uint64) {
	if m.onCopy != nil && !m.copied {
		m.copied = true
		m.onCopy()
	}
	m.real.CopyRange(dst, start, size)
}

// Test_Reader_Read_DeterministicOverwrite verifies that a record evicted
// during a read is never returned and that the reader recovers afterwards.
//
// The eviction happens either between the first position load and the copy,
// or between the copy and the recheck.
func Test_Reader_Read_DeterministicOverwrite(t *testing.T) {
	cases := []struct {
		name string
		arm  func(src *hookedSource, evict func())
	}{
		{
			name: "invalidates between snapshot and copy",
			arm:  func(src *hookedSource, evict func()) { src.onCopy = evict },
		},
		{
			name: "invalidates between copy and recheck",
			arm: func(src *hookedSource, evict func()) {
				// Load 2 is the recheck of the first read.
				src.onIndices = func(call int) {
					if call == 2 {
						evict()
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newTestAgent(t, 1)
			object := newRingObject(t, agent, "overwrite", 64)
			writer := newWriter(t, object, 0)

			_, err := writer.WriteRecord([]byte("stale"))
			require.NoError(t, err)

			real := source(t, object, 0)

			evict := func() {
				// The write is big enough to evict the "stale" record. The
				// readable position moves past everything this read saw.
				//
				// The payload is all frame-size words. So torn or stale bytes
				// never fail the length check. Only the recheck can reject
				// them, and that is what this test checks.
				_, evictErr := writer.WriteRecord(repeatedFrameSizePayload(48))
				require.NoError(t, evictErr)
			}

			hooked := &hookedSource{real: real}
			tc.arm(hooked, evict)

			reader, err := cring.NewReader(0, object.Capacity(), hooked)
			require.NoError(t, err)
			records := reader.Read(1024)
			require.Empty(t, records, "an invalidated record must never be returned")

			_, err = writer.WriteRecord([]byte("fresh"))
			require.NoError(t, err)

			records = reader.Read(1024)
			require.Len(t, records, 1)
			require.Equal(t, []byte("fresh"), records[0].Bytes)
		})
	}
}

// Test_Reader_Read_PartialPrefixDropKeepsSurvivingRecord verifies that
// evicting the older of two buffered records keeps the younger one intact.
func Test_Reader_Read_PartialPrefixDropKeepsSurvivingRecord(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "partial-drop", 128)
	writer := newWriter(t, object, 0)

	_, err := writer.WriteRecord([]byte("AAAAAAAA")) // stale: occupies [0,16)
	require.NoError(t, err)
	_, err = writer.WriteRecord([]byte("survive-me!!")) // occupies [16,36)
	require.NoError(t, err)

	real := source(t, object, 0)

	hooked := &hookedSource{real: real}
	hooked.onIndices = func(call int) {
		// Load 2 is the recheck of the read below.
		if call != 2 {
			return
		}
		// 96 bytes do not fit in the 92 free bytes, so the write evicts
		// exactly the stale record.
		//
		// The new bytes do not overwrite the surviving record, and the
		// reader does not drop it.
		_, evictErr := writer.WriteRecord(repeatedFrameSizePayload(88))
		require.NoError(t, evictErr)
	}

	reader, err := cring.NewReader(0, object.Capacity(), hooked)
	require.NoError(t, err)
	records := reader.Read(1024)
	require.Len(t, records, 1, "only the invalidated prefix must be dropped")
	require.Equal(t, []byte("survive-me!!"), records[0].Bytes)
}

// Test_Reader_Read_InvalidatesCarriedPartialRecord verifies that the recheck
// also drops the bytes of a record partly buffered by an earlier read.
func Test_Reader_Read_InvalidatesCarriedPartialRecord(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "carried-partial", 64)
	writer := newWriter(t, object, 0)

	// A 24-byte record of frame-size words.
	//
	// If the reader kept the rest of it at a wrong offset, it would still
	// parse as a valid record and not fail the length check.
	_, err := writer.WriteRecord(repeatedFrameSizePayload(16))
	require.NoError(t, err)

	real := source(t, object, 0)

	hooked := &hookedSource{real: real}
	hooked.onIndices = func(call int) {
		// Load 4 is the recheck of the second read.
		//
		// Loads 1 and 2 are the first load and the recheck of the first
		// read below, where the recheck finds nothing.
		if call != 4 {
			return
		}
		// 48 bytes do not fit in the 40 free bytes while the 24-byte
		// record is still in the ring. So the write evicts that record.
		_, evictErr := writer.WriteRecord(repeatedFrameSizePayload(40))
		require.NoError(t, evictErr)
	}

	reader, err := cring.NewReader(0, object.Capacity(), hooked)
	require.NoError(t, err)

	// The read copies only the frame and the first payload word, 12 of
	// the 24 bytes. The reader keeps them for the next call.
	require.Empty(t, reader.Read(12))

	records := reader.Read(1024)
	require.Empty(t, records, "the carried prefix plus the newly copied bytes must both be dropped")

	_, err = writer.WriteRecord([]byte("fresh"))
	require.NoError(t, err)

	// The evicting write committed a real record after the dropped one.
	// It comes before "fresh". Only the partly read record must be gone.
	records = reader.Read(1024)
	require.Len(t, records, 2)
	require.Equal(t, repeatedFrameSizePayload(40), records[0].Bytes)
	require.Equal(t, []byte("fresh"), records[1].Bytes)
}

// Test_Reader_Read_DropExceedsBufferDiscardsEverything verifies that an
// eviction past the copied bytes clears the reader's whole buffer.
func Test_Reader_Read_DropExceedsBufferDiscardsEverything(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "exceeds-buffer", 64)
	writer := newWriter(t, object, 0)

	_, err := writer.WriteRecord([]byte("AAAAAAAA")) // occupies [0,16)
	require.NoError(t, err)
	_, err = writer.WriteRecord([]byte("BBBBBBBB")) // occupies [16,32)
	require.NoError(t, err)

	real := source(t, object, 0)

	hooked := &hookedSource{real: real}
	hooked.onCopy = func() {
		// 40 bytes do not fit in the 32 free bytes, so the write evicts
		// exactly the first record.
		//
		// The readable position moves past the 10 copied bytes.
		_, evictErr := writer.WriteRecord(repeatedFrameSizePayload(32))
		require.NoError(t, evictErr)
	}

	reader, err := cring.NewReader(0, object.Capacity(), hooked)
	require.NoError(t, err)
	records := reader.Read(10)
	require.Empty(t, records, "a drop past the copied range must clear the whole buffer, not slice past it")
}

// Test_Reader_Read_RecordBytesAppendDoesNotCorruptLaterRecords verifies
// that records come back in order with consecutive sequence numbers, and
// that an append to one payload never overwrites the next payload.
func Test_Reader_Read_RecordBytesAppendDoesNotCorruptLaterRecords(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "bytes-append", 128)
	writer := newWriter(t, object, 0)

	first, err := writer.WriteRecord([]byte("first"))
	require.NoError(t, err)
	second, err := writer.WriteRecord([]byte("second"))
	require.NoError(t, err)
	require.Equal(t, first+1, second)

	reader := openReader(t, object, 0)

	records := reader.Read(1024)
	require.Len(t, records, 2)
	require.Equal(t, first, records[0].Seqno)
	require.Equal(t, second, records[1].Seqno)
	for _, rec := range records {
		require.Equal(t, len(rec.Bytes), cap(rec.Bytes), "a payload must have no spare capacity")
	}

	// Both payloads share one block. This append would fit in the space of
	// the second payload if the first one had spare capacity, so it would
	// overwrite "second" in place.
	_ = append(records[0].Bytes, []byte("XXXXXX")...)

	require.Equal(t, []byte("first"), records[0].Bytes)
	require.Equal(t, []byte("second"), records[1].Bytes)
}

// Test_Reader_Read_EvictionBetweenReadsDropsCarriedPartial verifies that an
// eviction between two reads drops the partly read record.
func Test_Reader_Read_EvictionBetweenReadsDropsCarriedPartial(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "evict-between", 64)
	writer := newWriter(t, object, 0)

	// A 24-byte record of frame-size words. Stale bytes would parse as
	// valid records and not fail the length check.
	_, err := writer.WriteRecord(repeatedFrameSizePayload(16))
	require.NoError(t, err)

	reader := openReader(t, object, 0)
	require.Empty(t, reader.Read(12), "a partial record must stay buffered")

	// 48 bytes do not fit in the 40 free bytes. So this write evicts the
	// partly read record before the next read.
	payload := repeatedFrameSizePayload(40)
	seqno, err := writer.WriteRecord(payload)
	require.NoError(t, err)

	records := reader.Read(1024)
	require.Len(t, records, 1, "only the record written after the eviction may be returned")
	require.Equal(t, seqno, records[0].Seqno)
	require.Equal(t, payload, records[0].Bytes)
}

// Test_Reader_Read_SteadyStateAllocatesOnlyReturnedRecords verifies that a
// read after warm-up allocates only for the records it returns.
func Test_Reader_Read_SteadyStateAllocatesOnlyReturnedRecords(t *testing.T) {
	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "allocs", 4096)
	writer := newWriter(t, object, 0)

	reader := openReader(t, object, 0)

	// 56 payload bytes make a 64-byte record. Four reads of 16 bytes get
	// it, and only the last one completes it.
	payload := make([]byte, 56)
	cycle := func() int {
		_, _ = writer.WriteRecord(payload)
		returned := 0
		for range 4 {
			returned += len(reader.Read(16))
		}
		return returned
	}
	require.Equal(t, 1, cycle(), "warm-up must return the record")

	returned := 0
	allocs := testing.AllocsPerRun(100, func() {
		returned += cycle()
	})
	require.Equal(t, 101, returned, "every cycle must return exactly one record")
	// The call that completes the record allocates one record slice and
	// one payload block.
	require.LessOrEqual(t, allocs, 2.0)

	_, err := writer.WriteRecord(payload)
	require.NoError(t, err)
	partial := testing.AllocsPerRun(1, func() {
		require.Empty(t, reader.Read(8))
	})
	require.Zero(t, partial, "a call completing no record must not allocate")
}

// Test_Reader_NewReader_RejectsCapacityBelowFrame verifies that a reader is
// refused when the capacity cannot hold even one record frame.
func Test_Reader_NewReader_RejectsCapacityBelowFrame(t *testing.T) {
	_, err := cring.NewReader(0, cring.RecordFrameSize-1, nil)
	require.Error(t, err)

	_, err = cring.NewReader(0, cring.RecordFrameSize, nil)
	require.NoError(t, err)
}

// Test_Reader_Stress_ConcurrentWriterNeverTears verifies that the reader
// never returns a torn record while the C writer overwrites at full speed.
//
// The ring is small, so it stays full and the writer evicts all the time.
func Test_Reader_Stress_ConcurrentWriterNeverTears(t *testing.T) {
	if testing.Short() {
		t.Skip("the concurrent stress is skipped in short mode")
	}
	const (
		records  = 1 << 22
		capacity = 4096
	)

	agent := newTestAgent(t, 1)
	object := newRingObject(t, agent, "stress", capacity)
	writer := newWriter(t, object, 0)
	reader := openReader(t, object, 0)

	stress, err := writer.StartStress(records)
	require.NoError(t, err)

	var returned, torn uint64
	check := func(records []cring.Record) {
		for _, rec := range records {
			returned++
			if !ringtest.StressRecordValid(rec.Seqno, rec.Bytes) {
				torn++
				if torn <= 10 {
					t.Logf("torn record: seqno=%d len=%d", rec.Seqno, len(rec.Bytes))
				}
			}
		}
	}
	for {
		done := stress.Done()
		check(reader.Read(capacity))
		if done && !reader.HasMore() {
			break
		}
	}
	written := stress.Written()
	stress.Wait()

	t.Logf("written=%d returned=%d torn=%d", written, returned, torn)
	require.Equal(t, uint64(records), written, "the writer must commit every record")
	require.NotZero(t, returned, "the reader must keep up with some records")
	require.Zero(t, torn, "no torn record may ever be returned")
}

// benchReadBudget is the byte limit of one read in the benchmarks.
//
// It matches pdump's default read chunk.
const benchReadBudget = 512 << 10

// benchRingCapacity is the ring size in the benchmarks, pdump's minimum.
const benchRingCapacity = 1 << 20

// benchRecordSizes are the record lengths in the benchmarks, frame
// included.
var benchRecordSizes = []uint32{64, 1500}

// readerBenchStats accumulates what a benchmarked reader returned.
type readerBenchStats struct {
	records uint64
	bytes   uint64
}

func (m *readerBenchStats) add(records []cring.Record) {
	m.records += uint64(len(records))
	for _, rec := range records {
		m.bytes += uint64(cring.RecordFrameSize) + uint64(len(rec.Bytes))
	}
}

// report reports the cost per record and the throughput over the timed
// part of the run.
func (m *readerBenchStats) report(b *testing.B) {
	b.Helper()
	if m.records == 0 {
		b.Fatal("the reader returned no records")
	}
	secs := b.Elapsed().Seconds()
	b.ReportMetric(secs*1e9/float64(m.records), "ns/record")
	b.ReportMetric(float64(m.records)/secs, "records/s")
	b.ReportMetric(float64(m.bytes)/secs/1e6, "MB/s")
}

// Benchmark_Reader_Read_Prefilled measures the reader's own cost while no
// writer runs.
//
// The cost covers the copy out of the shared-memory ring, the recheck and
// the parsing, per record and per byte. Each pass fills the ring with the
// timer stopped. Then it reads the ring until empty, with reads of the
// size pdump uses.
func Benchmark_Reader_Read_Prefilled(b *testing.B) {
	for _, size := range benchRecordSizes {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			agent := newTestAgent(b, 1)
			object := newRingObject(b, agent, "bench", benchRingCapacity)
			writer := newWriter(b, object, 0)
			reader := openReader(b, object, 0)

			payload := make([]byte, size-cring.RecordFrameSize)
			perPass := benchRingCapacity / ((size + 3) &^ 3)

			var stats readerBenchStats
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				for range perPass {
					if _, err := writer.WriteRecord(payload); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()

				for reader.HasMore() {
					stats.add(reader.Read(benchReadBudget))
				}
			}
			b.StopTimer()
			if want := uint64(b.N) * uint64(perPass); stats.records != want {
				b.Fatalf("read %d records, want %d", stats.records, want)
			}
			stats.report(b)
		})
	}
}

// memSource is an in-memory record source whose positions a test sets
// directly.
//
// It records the start and size of every copy, so a test can see where the
// reader reads.
type memSource struct {
	data            []byte
	write, readable uint64
	copies          [][2]uint64
}

func (m *memSource) Indices() (uint64, uint64) {
	return m.write, m.readable
}

func (m *memSource) CopyRange(dst []byte, start, size uint64) {
	if uint64(len(dst)) != size {
		panic(fmt.Sprintf("copy of %d bytes into %d", size, len(dst)))
	}
	m.copies = append(m.copies, [2]uint64{start, size})
	mask := uint64(len(m.data) - 1)
	for idx := range dst {
		dst[idx] = m.data[(start+uint64(idx))&mask]
	}
}

// fuzzStep is the encoded size of one fuzz step: the write and readable
// positions as 32-bit words and the read budget as a 16-bit word.
const fuzzStep = 10

// fuzzFrames returns a ring of the given capacity with frames of the given
// lengths laid out from a logical offset, each at the next 4-byte boundary.
func fuzzFrames(capacity, offset uint32, lengths ...uint32) []byte {
	ring := make([]byte, capacity)
	for seqno, length := range lengths {
		frame := binary.LittleEndian.AppendUint32(nil, length)
		frame = binary.LittleEndian.AppendUint32(frame, uint32(seqno))
		for idx, b := range frame {
			ring[(offset+uint32(idx))&(capacity-1)] = b
		}
		offset += (max(length, cring.RecordFrameSize) + 3) &^ 3
	}
	return ring
}

// fuzzSteps encodes steps given as write, readable and budget triples.
func fuzzSteps(triples ...uint32) []byte {
	var steps []byte
	for idx := 0; idx+2 < len(triples); idx += 3 {
		steps = binary.LittleEndian.AppendUint32(steps, triples[idx])
		steps = binary.LittleEndian.AppendUint32(steps, triples[idx+1])
		steps = binary.LittleEndian.AppendUint16(steps, uint16(triples[idx+2]))
	}
	return steps
}

// Fuzz_Reader_Read verifies that the reader stays sound for any ring bytes
// and any positions a corrupt or racing writer may publish.
//
// Every read must return without a panic. With a nonzero budget it makes
// progress while data is pending. Its copies never move behind earlier ones,
// and records never claim more bytes than were copied. Each record fits the
// ring, owns its payload capacity and carries the reader's worker.
func Fuzz_Reader_Read(f *testing.F) {
	// Capacity 64 is shift 3. Valid records, read whole and then in pieces
	// that carry a partial record over, also across the physical end.
	f.Add(byte(3), fuzzFrames(64, 0, 8, 12, 17), fuzzSteps(40, 0, 1024))
	f.Add(byte(3), fuzzFrames(64, 0, 8, 12, 17), fuzzSteps(40, 0, 5, 40, 0, 13, 40, 0, 1024))
	f.Add(byte(3), fuzzFrames(64, 52, 8, 12, 17), fuzzSteps(92, 52, 7, 92, 52, 1024))
	for _, length := range []uint32{0, 4, 7, 8, 64, 65, 0xffffffff} {
		f.Add(byte(3), fuzzFrames(64, 0, length), fuzzSteps(64, 0, 1024, 128, 64, 1024))
	}
	// Readable ahead of write, write far ahead of readable, an empty ring.
	f.Add(byte(3), fuzzFrames(64, 0, 8), fuzzSteps(8, 100, 1024, 8, 0, 1024))
	f.Add(byte(3), fuzzFrames(64, 0, 8, 8), fuzzSteps(640, 0, 1024, 640, 600, 1024))
	f.Add(byte(0), []byte(nil), fuzzSteps(0, 0, 1024))

	f.Fuzz(func(t *testing.T, capShift byte, ring []byte, steps []byte) {
		const worker = 7

		capacity := uint32(8) << (capShift % 10)
		src := &memSource{data: make([]byte, capacity)}
		copy(src.data, ring)
		reader, err := cring.NewReader(worker, capacity, src)
		require.NoError(t, err)

		// The step cap bounds the run; each read is linear in its budget.
		var cursor, copied, returned uint64
		for n := 0; n < 64 && len(steps) >= fuzzStep; n++ {
			src.write = uint64(binary.LittleEndian.Uint32(steps[0:4]))
			src.readable = uint64(binary.LittleEndian.Uint32(steps[4:8]))
			maxBytes := uint32(binary.LittleEndian.Uint16(steps[8:10])) % (4*capacity + 1)
			steps = steps[fuzzStep:]

			hadMore := reader.HasMore()
			src.copies = src.copies[:0]
			records := reader.Read(maxBytes)

			for _, c := range src.copies {
				require.GreaterOrEqual(t, c[0], cursor, "copy moved behind an earlier one")
				cursor = c[0] + c[1]
				copied += c[1]
			}
			if hadMore && maxBytes > 0 {
				require.True(t, len(src.copies) > 0 || !reader.HasMore(), "read made no progress")
			}
			for _, rec := range records {
				require.Equal(t, uint16(worker), rec.Worker)
				require.LessOrEqual(t, uint64(len(rec.Bytes))+uint64(cring.RecordFrameSize), uint64(capacity))
				require.Equal(t, len(rec.Bytes), cap(rec.Bytes))
				returned += uint64(len(rec.Bytes)) + uint64(cring.RecordFrameSize)
			}
			require.LessOrEqual(t, returned, copied, "records claim more bytes than were copied")
		}
	})
}
