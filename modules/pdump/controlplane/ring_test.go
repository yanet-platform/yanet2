package pdump

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb"
)

// Test constants and helpers
const (
	testRingSize      = 1024
	testReadChunkSize = 512
)

// testWorkerWrapper wraps workerArea with C ring buffer for testing
type testWorkerWrapper struct {
	cRing *cRingBuffer
	wa    *workerArea
}

// testRingBufferWrapper wraps ringBuffer with C ring buffers for testing
type testRingBufferWrapper struct {
	rb *ringBuffer
	ww []*testWorkerWrapper
}

// createTestWorker creates a worker area for testing
func createTestWorker(t *testing.T, size int) *testWorkerWrapper {
	t.Helper()

	// Create C ring buffer structure
	cRing := &cRingBuffer{
		size: _Ctype_uint32_t(size),
		mask: _Ctype_uint32_t(size - 1),
	}

	worker := &workerArea{
		writeIdx:    (*uint64)(&cRing.write_idx),
		readableIdx: (*uint64)(&cRing.readable_idx),
		readIdx:     0,
		data:        make([]byte, size),
		mask:        uint64(size - 1),
		log:         zaptest.NewLogger(t),
		buf:         make([]byte, 0, size),
	}

	return &testWorkerWrapper{
		wa:    worker,
		cRing: cRing,
	}
}

type ringBufferOption func(*ringBufferConfig)

type ringBufferConfig struct {
	ringSize int
}

func WithRingSize(size int) ringBufferOption {
	return func(c *ringBufferConfig) {
		c.ringSize = size
	}
}

// createTestRingBuffer creates a ring buffer for testing
func createTestRingBuffer(t *testing.T, numWorkers int, opts ...ringBufferOption) *testRingBufferWrapper {
	t.Helper()

	config := &ringBufferConfig{
		ringSize: testRingSize,
	}

	for _, opt := range opts {
		opt(config)
	}

	workers := make([]*workerArea, numWorkers)
	workerWrappers := make([]*testWorkerWrapper, numWorkers)
	for i := range numWorkers {
		wrapper := createTestWorker(t, config.ringSize)
		workers[i] = wrapper.wa
		workerWrappers[i] = wrapper
	}
	return &testRingBufferWrapper{
		rb: &ringBuffer{
			workers:       workers,
			perWorkerSize: uint32(config.ringSize),
			readChunkSize: testReadChunkSize,
		},
		ww: workerWrappers,
	}
}

// writeTestMessage writes a test message to the ring buffer at the specified offset
func writeTestMessage(t *testing.T, wrapper *testWorkerWrapper, payload []byte) int {
	t.Helper()

	msg := ringMsgHdr{
		magic:        ringMsgMagic,
		total_len:    _Ctype_uint32_t(unsafe.Sizeof(ringMsgHdr{}) + uintptr(len(payload))),
		timestamp:    1234567890,
		packet_len:   _Ctype_uint32_t(len(payload)),
		worker_idx:   1,
		pipeline_idx: 2,
		rx_device_id: 3,
		tx_device_id: 4,
		queue:        _Ctype_uint8_t(defaultMode),
	}

	var payloadPtr *uint8
	if len(payload) > 0 {
		payloadPtr = &payload[0]
	}

	pinner := runtime.Pinner{}
	pinner.Pin(wrapper.wa)
	defer pinner.Unpin()
	forTestsPdumpRingWriteMsg(
		wrapper.cRing,
		&wrapper.wa.data[0],
		&msg,
		payloadPtr,
	)

	return alignToU32(int(msg.total_len))
}

// TestAlignToU32 tests the alignment function
func TestAlignToU32(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"already aligned", 0, 0},
		{"already aligned 4", 4, 4},
		{"already aligned 8", 8, 8},
		{"needs alignment +1", 1, 4},
		{"needs alignment +2", 2, 4},
		{"needs alignment +3", 3, 4},
		{"needs alignment +5", 5, 8},
		{"needs alignment +9", 9, 12},
		{"large number", 1023, 1024},
		{"large aligned", 1024, 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := alignToU32(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestRingBufferClone tests the Clone functionality
func TestRingBufferClone(t *testing.T) {
	original := createTestRingBuffer(t, 2)
	*original.rb.workers[0].readableIdx = 96
	*original.rb.workers[0].writeIdx = 150
	*original.rb.workers[0].readableIdx = 128
	*original.rb.workers[1].writeIdx = 250

	clone := original.rb.Clone()

	// Verify basic properties are copied
	assert.Equal(t, original.rb.perWorkerSize, clone.perWorkerSize)
	assert.Equal(t, original.rb.readChunkSize, clone.readChunkSize)
	assert.Equal(t, len(original.rb.workers), len(clone.workers))

	// Verify worker areas are properly cloned
	for i := range original.rb.workers {
		// writeIdx and readableIdx should point to the same location for all clones
		assert.Equal(t, unsafe.Pointer(original.rb.workers[i].readableIdx), unsafe.Pointer(clone.workers[i].readableIdx))
		assert.Equal(t, unsafe.Pointer(original.rb.workers[i].writeIdx), unsafe.Pointer(clone.workers[i].writeIdx))
		assert.Equal(t, original.rb.workers[i].mask, clone.workers[i].mask)
		assert.Equal(t, len(original.rb.workers[i].data), len(clone.workers[i].data))
	}
}

// TestWorkerAreaHasMore tests the hasMore functionality
func TestWorkerAreaHasMore(t *testing.T) {
	tests := []struct {
		name     string
		writeIdx uint64
		readIdx  uint64
		expected bool
	}{
		{
			name:     "no new data",
			writeIdx: 100,
			readIdx:  100,
			expected: false,
		},
		{
			name:     "has new data",
			writeIdx: 200,
			readIdx:  100,
			expected: true,
		},
		{
			name:     "reader ahead of writer",
			writeIdx: 100,
			readIdx:  200,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker := createTestWorker(t, testRingSize).wa
			*worker.writeIdx = tt.writeIdx
			worker.readIdx = tt.readIdx

			assert.Equal(t, tt.expected, worker.hasMore())
		})
	}

	t.Run("concurrent access", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize).wa

		var wg sync.WaitGroup
		results := make([]bool, 100)

		// Start multiple goroutines checking hasMore
		wg.Add(100)
		for i := range 100 {
			go func(idx int) {
				defer wg.Done()
				results[idx] = worker.hasMore()
			}(i)
		}

		// Concurrently update write index
		go func() {
			for range 50 {
				atomic.AddUint64(worker.writeIdx, 1)
				time.Sleep(time.Microsecond)
			}
		}()

		wg.Wait() // Should not panic (run with race detector)
	})
}

// TestWorkerAreaRead tests the read functionality
func TestWorkerAreaRead(t *testing.T) {
	t.Run("read single message", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		payload := []byte("ABC")

		writeTestMessage(t, worker, payload)

		// Debug: Check ring buffer state after writing
		writeIdx := *worker.wa.writeIdx
		readableIdx := *worker.wa.readableIdx
		t.Logf("After writeTestMessage: writeIdx=%d, readableIdx=%d, readIdx=%d",
			writeIdx, readableIdx, worker.wa.readIdx)

		records := worker.wa.read(testReadChunkSize)
		require.Len(t, records, 1)

		record := records[0]
		assert.Equal(t, uint64(1234567890), record.Meta.Timestamp)
		assert.Equal(t, uint32(len(payload)), record.Meta.DataSize)
		assert.Equal(t, uint32(len(payload)), record.Meta.PacketLen)
		assert.Equal(t, uint32(1), record.Meta.WorkerIdx)
		assert.Equal(t, uint32(2), record.Meta.PipelineIdx)
		assert.Equal(t, uint32(3), record.Meta.RxDeviceId)
		assert.Equal(t, uint32(4), record.Meta.TxDeviceId)
		assert.False(t, record.Meta.Queue&_Ciconst_PDUMP_DROPS != 0, "meta.queue %b", record.Meta.Queue)
		assert.True(t, record.Meta.Queue&_Ciconst_PDUMP_INPUT != 0)
		assert.Equal(t, payload, record.Data)
	})

	t.Run("read multiple messages", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)

		payload1 := []byte("AB")
		payload2 := []byte("CD")

		writeTestMessage(t, worker, payload1)
		writeTestMessage(t, worker, payload2)

		records := worker.wa.read(testReadChunkSize)
		require.Len(t, records, 2)

		assert.Equal(t, payload1, records[0].Data)
		assert.Equal(t, payload2, records[1].Data)
	})

	t.Run("incomplete message", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		payload := []byte("BCD")

		writeTestMessage(t, worker, payload)

		records := worker.wa.read(40 /*too small chunk size*/)
		assert.Empty(t, records, "Should not return incomplete messages")
	})

	t.Run("invalid magic number", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		payload := []byte{0x42}

		// First write a valid message
		writeTestMessage(t, worker, payload)

		// Then corrupt the magic number in the ring buffer data
		msgHeader := (*ringMsgHdr)(unsafe.Pointer(&worker.wa.data[0]))
		msgHeader.magic = 0xBADC0DE // Invalid magic

		records := worker.wa.read(testReadChunkSize)
		assert.Empty(t, records, "Should not return records with invalid magic")
		assert.Empty(t, worker.wa.buf, "Buffer should be cleared after invalid magic")
	})

	t.Run("invalid total length", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		payload := []byte{0x42}

		// First write a valid message
		writeTestMessage(t, worker, payload)

		// Then corrupt the total_len in the ring buffer data
		msgHeader := (*ringMsgHdr)(unsafe.Pointer(&worker.wa.data[0]))
		msgHeader.total_len = _Ctype_uint32_t(unsafe.Sizeof(ringMsgHdr{}) - 1) // Too small

		records := worker.wa.read(testReadChunkSize)
		assert.Empty(t, records, "Should not return records with invalid total_len")
		assert.Empty(t, worker.wa.buf, "Buffer should be cleared after invalid total_len")
	})

	t.Run("wraparound read", func(t *testing.T) {
		const smallRingSize = 64
		worker := createTestWorker(t, smallRingSize)
		worker.wa.mask = smallRingSize - 1

		payload := []byte("ABCD")
		headerSize := int(unsafe.Sizeof(ringMsgHdr{}))

		// Position the ring buffer to force wraparound by advancing write_idx
		startOffset := smallRingSize - headerSize + 2 // Header will wrap
		*worker.wa.writeIdx = uint64(startOffset)
		*worker.wa.readableIdx = uint64(startOffset)

		// Write message using the proper function - it will handle wraparound
		writeTestMessage(t, worker, payload)

		records := worker.wa.read(uint32(smallRingSize))
		require.Len(t, records, 1)

		record := records[0]
		assert.Equal(t, payload, record.Data)
		assert.Equal(t, uint64(1234567890), record.Meta.Timestamp)
	})

	t.Run("no data available", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		*worker.wa.writeIdx = 100
		*worker.wa.readableIdx = 100
		worker.wa.readIdx = 100

		records := worker.wa.read(testReadChunkSize)
		assert.Empty(t, records)
	})

	t.Run("read with chunk size limit", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)

		// Create multiple messages
		for i := range 10 {
			payload := []byte{byte(i)}
			writeTestMessage(t, worker, payload)
		}

		// Read with small chunk size
		records := worker.wa.read(100) // Small chunk size

		// Should read some but not all messages due to chunk size limit
		assert.Greater(t, len(records), 0)
		assert.LessOrEqual(t, len(records), 10)
	})
}

// TestWorkerAreaReadAdditional tests additional read scenarios
func TestWorkerAreaReadAdditional(t *testing.T) {
	t.Run("drops flag handling", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		payload := []byte{0x42}

		// First write a valid message
		writeTestMessage(t, worker, payload)

		// Then modify the is_drops flag in the ring buffer data
		msgHeader := (*ringMsgHdr)(unsafe.Pointer(&worker.wa.data[0]))
		msgHeader.queue = _Ciconst_PDUMP_DROPS // Set drops flag

		records := worker.wa.read(testReadChunkSize)
		require.Len(t, records, 1)

		assert.True(t, records[0].Meta.Queue&_Ciconst_PDUMP_DROPS != 0, "Should correctly handle drops flag")
	})
}

// TestWorkerAreaOverwriteHandling tests overwrite detection and handling
func TestWorkerAreaOverwriteHandling(t *testing.T) {
	t.Run("complete overwrite", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)

		payload := []byte{0x42}
		writeTestMessage(t, worker, payload)
		// Simulate complete overwrite
		*worker.wa.readableIdx = *worker.wa.writeIdx + 500 // Overwrite everything

		records := worker.wa.read(testReadChunkSize)
		assert.Empty(t, records)
		assert.Empty(t, worker.wa.buf, "Buffer should be cleared after complete overwrite")
	})

	t.Run("reader behind readable index", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		worker.wa.readIdx = 50
		*worker.wa.readableIdx = 100 // Reader is behind
		*worker.wa.writeIdx = 200

		// Add stale data to buffer
		worker.wa.buf = append(worker.wa.buf, []byte{0x01, 0x02, 0x03}...)

		worker.wa.read(testReadChunkSize)

		// Should catch up reader position and clear stale buffer
		assert.Equal(t, 200, int(worker.wa.readIdx))
		assert.True(t, len(worker.wa.buf) < 3) // Original buffer should be cleared
	})
}

// TestRingBufferSpawnWakers tests the waker functionality
func TestRingBufferSpawnWakers(t *testing.T) {
	t.Run("basic waker functionality", func(t *testing.T) {
		rb := createTestRingBuffer(t, 2)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		wakers := rb.rb.spawnWakers(ctx)
		require.Len(t, wakers, 2)

		// Add data to first worker
		atomic.StoreUint64(rb.rb.workers[0].writeIdx, 100)
		atomic.StoreUint64(&rb.rb.workers[0].readIdx, 50)

		// Should receive notification
		select {
		case <-wakers[0]:
			// Expected
		case <-time.After(50 * time.Millisecond):
			t.Fatal("Should have received waker notification")
		}
	})

	t.Run("waker stops on context cancellation", func(t *testing.T) {
		rb := createTestRingBuffer(t, 1)
		ctx, cancel := context.WithCancel(context.Background())

		wakers := rb.rb.spawnWakers(ctx)
		require.Len(t, wakers, 1)

		// Cancel context
		cancel()

		// Give some time for goroutine to stop
		time.Sleep(5 * time.Millisecond)

		// Add data - should not receive notification after context cancellation
		atomic.StoreUint64(rb.rb.workers[0].writeIdx, 100)
		atomic.StoreUint64(&rb.rb.workers[0].readIdx, 50)

		select {
		case <-wakers[0]:
			t.Fatal("Should not receive notification after context cancellation")
		case <-time.After(10 * time.Millisecond):
			// Expected - no notification
		}
	})
}

// TestRingBufferRunReaders tests the reader functionality
func TestRingBufferRunReaders(t *testing.T) {
	t.Run("basic reader functionality", func(t *testing.T) {
		rb := createTestRingBuffer(t, 1)

		payload := []byte{0x42, 0x43}
		writeTestMessage(t, rb.ww[0], payload)

		recordCh := make(chan *pdumppb.Record, 10)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		wg, _ := errgroup.WithContext(context.Background())
		wg.Go(func() error {
			err := rb.rb.runReaders(ctx, recordCh)
			t.Logf("runReaders return: %v", err)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			return nil
		})

		// Should receive the record
		select {
		case record := <-recordCh:
			assert.Equal(t, payload, record.Data)
			assert.Equal(t, uint32(len(payload)), record.Meta.DataSize)
		case <-time.After(50 * time.Millisecond):
			t.Fatal("Should have received record")
		}
		wg.Wait()
	})

	t.Run("multiple workers", func(t *testing.T) {
		rb := createTestRingBuffer(t, 3)

		// Add data to each worker
		for i := range rb.rb.workers {
			payload := []byte{byte(i + 1)}
			writeTestMessage(t, rb.ww[i], payload)
		}

		recordCh := make(chan *pdumppb.Record, 10)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		wg, _ := errgroup.WithContext(context.Background())
		wg.Go(func() error {
			err := rb.rb.runReaders(ctx, recordCh)
			t.Logf("runReaders return: %v", err)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			return nil
		})

		// Should receive records from all workers
		receivedData := make(map[byte]bool)
		for range 3 {
			select {
			case record := <-recordCh:
				require.Len(t, record.Data, 1)
				receivedData[record.Data[0]] = true
			case <-time.After(50 * time.Millisecond):
				t.Fatal("Should have received all records")
			}
		}
		wg.Wait()

		assert.True(t, receivedData[1])
		assert.True(t, receivedData[2])
		assert.True(t, receivedData[3])
	})

	t.Run("context cancellation", func(t *testing.T) {
		rb := createTestRingBuffer(t, 1)
		recordCh := make(chan *pdumppb.Record, 10)
		ctx, cancel := context.WithCancel(context.Background())

		errCh := make(chan error, 1)
		go func() {
			errCh <- rb.rb.runReaders(ctx, recordCh)
		}()

		// Cancel context
		cancel()

		// Should return context error
		select {
		case err := <-errCh:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(50 * time.Millisecond):
			t.Fatal("Should have returned context error")
		}
	})

	t.Run("continuous reading", func(t *testing.T) {
		rb := createTestRingBuffer(t, 1)
		recordCh := make(chan *pdumppb.Record, 100)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		runReadersGo := make(chan bool)
		go func() {
			close(runReadersGo)
			rb.rb.runReaders(ctx, recordCh)
		}()

		// Continuously add data
		go func() {
			<-runReadersGo
			// Wait for runReaders to spawn and fall into waiting state for waker notifications
			time.Sleep(10 * time.Millisecond)
			for i := range 10 {
				payload := []byte{byte(i)}
				writeTestMessage(t, rb.ww[0], payload)
				time.Sleep(time.Millisecond)
			}
		}()

		// Should receive multiple records
		recordCount := 0
		timeout := time.After(50 * time.Millisecond)
		for {
			select {
			case <-recordCh:
				recordCount++
			case <-timeout:
				assert.Greater(t, recordCount, 0, "Should have received some records")
				return
			}
		}
	})
}

// TestRingBufferConcurrency tests concurrent operations
func TestRingBufferConcurrency(t *testing.T) {
	t.Run("concurrent readers", func(t *testing.T) {
		rb := createTestRingBuffer(t, 2)

		// Add data to both workers
		for workerIdx := range rb.rb.workers {
			for j := range 5 {
				payload := []byte{byte(workerIdx*10 + j)}
				writeTestMessage(t, rb.ww[workerIdx], payload)
			}
		}

		recordCh := make(chan *pdumppb.Record, 100)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()

		// Start multiple reader instances. The ring buffer contains 10 messages,
		// so each reader can read at most 10 messages. For the test to pass,
		// the other readers must read their own copy of the ring buffer.
		var wg sync.WaitGroup
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rbClone := rb.rb.Clone()
				rbClone.runReaders(ctx, recordCh)
			}()
		}

		go func() {
			wg.Wait()
			close(recordCh)
		}()

		expectedRecordCount := 20
		// Count received records
		recordCount := 0
		for range recordCh {
			recordCount++
			if recordCount == expectedRecordCount {
				break
			}
		}

		// Should receive records from all readers
		assert.Equal(t, recordCount, expectedRecordCount, "Should receive exactly two copies of the ring buffer from concurrent readers")
	})

	t.Run("concurrent hasMore calls", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)

		var wg sync.WaitGroup
		results := make([]bool, 1000)

		// Start many concurrent hasMore calls
		for i := range 1000 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx] = worker.wa.hasMore()
			}(i)
		}

		// Concurrently modify indices
		go func() {
			for range 100 {
				atomic.AddUint64(worker.wa.writeIdx, 1)
				atomic.AddUint64(&worker.wa.readIdx, 1)
			}
		}()

		wg.Wait()
		// Should not panic and should return valid results
		for _, result := range results {
			_ = result // Just ensure no panic
		}
	})
}

// TestRingBufferEdgeCases tests various edge cases
func TestRingBufferEdgeCases(t *testing.T) {
	t.Run("zero-length payload", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		payload := []byte{}

		writeTestMessage(t, worker, payload)

		records := worker.wa.read(testReadChunkSize)
		require.Len(t, records, 1)

		record := records[0]
		assert.Equal(t, uint32(0), record.Meta.DataSize)
		assert.Equal(t, []byte{}, record.Data)
	})

	t.Run("maximum payload size", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)
		// Create large payload that fits in ring buffer
		maxPayloadSize := testRingSize - int(unsafe.Sizeof(ringMsgHdr{})) - 64 // Leave some margin
		payload := make([]byte, maxPayloadSize)
		for i := range payload {
			payload[i] = byte(i % 256)
		}

		writeTestMessage(t, worker, payload)

		records := worker.wa.read(testReadChunkSize)
		require.Len(t, records, 0)
		// The message should be read in its entirety on the second attempt
		// since testReadChunkSize is too small to read it in one go
		records = worker.wa.read(testReadChunkSize)
		require.Len(t, records, 1)

		record := records[0]
		assert.Equal(t, uint32(len(payload)), record.Meta.DataSize)
		assert.Equal(t, payload, record.Data)
	})

	t.Run("buffer exactly at boundary", func(t *testing.T) {
		worker := createTestWorker(t, testRingSize)

		// Position message exactly at ring buffer boundary
		*worker.wa.writeIdx = testRingSize - 1
		payload := []byte{0x42}

		writeTestMessage(t, worker, payload)

		records := worker.wa.read(testReadChunkSize)
		require.Len(t, records, 1)
		assert.Equal(t, payload, records[0].Data)
	})

	t.Run("very small ring buffer", func(t *testing.T) {
		const verySmallSize = 64
		worker := createTestWorker(t, verySmallSize)
		worker.wa.mask = verySmallSize - 1

		payload := []byte{0x42}
		writeTestMessage(t, worker, payload)

		records := worker.wa.read(uint32(verySmallSize))
		require.Len(t, records, 1)
		assert.Equal(t, payload, records[0].Data)
	})
}

// TestRingBufferStressTest performs stress testing
func TestRingBufferStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	t.Run("high frequency reads", func(t *testing.T) {
		rb := createTestRingBuffer(t, 4, WithRingSize(64*1024))

		// Prepare data in all workers
		for workerIdx := range rb.rb.workers {
			for j := range 100 {
				payload := []byte{byte(j % 256)}
				writeTestMessage(t, rb.ww[workerIdx], payload)
			}
		}

		recordCh := make(chan *pdumppb.Record, 1000)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		go func() {
			rb.rb.runReaders(ctx, recordCh)
		}()

		// Count records received
		recordCount := 0
		timeout := time.After(50 * time.Millisecond)
		for {
			select {
			case <-recordCh:
				recordCount++
			case <-timeout:
				assert.Greater(t, recordCount, 300, "Should process many records under stress")
				return
			}
		}
	})

	t.Run("rapid context cancellations", func(t *testing.T) {
		rb := createTestRingBuffer(t, 2)

		for range 50 {
			recordCh := make(chan *pdumppb.Record, 10)
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)

			go func() {
				rb := rb.rb.Clone()
				rb.runReaders(ctx, recordCh)
			}()

			cancel()
			time.Sleep(time.Microsecond * 100)
		}
		// Should not panic or deadlock
	})
}

// BenchmarkWorkerAreaRead benchmarks the read performance
func BenchmarkWorkerAreaRead(b *testing.B) {
	worker := createTestWorker(&testing.T{}, testRingSize)
	worker.wa.log = zap.NewNop()

	// Prepare test data
	for i := range 100 {
		payload := []byte{byte(i % 256)}
		writeTestMessage(&testing.T{}, worker, payload)
	}

	b.ReportAllocs()

	for b.Loop() {
		worker.wa.readIdx = 0
		worker.wa.buf = worker.wa.buf[:0]
		records := worker.wa.read(testReadChunkSize)
		if len(records) == 0 {
			b.Fatal("No records read")
		}
	}
}
