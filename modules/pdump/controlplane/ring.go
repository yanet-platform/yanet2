package pdump

//#include "modules/pdump/api/controlplane.h"
import "C"

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb"
)

type ringMsgHdr C.struct_ring_msg_hdr
type cRingBuffer C.struct_ring_buffer

const (
	minRingSize     = datasize.MB
	hdrSizeSize     = int(unsafe.Sizeof(ringMsgHdr{}.total_len))
	cRingMsgHdrSize = unsafe.Sizeof(ringMsgHdr{})
)

var (
	maxRingSize          = uint32(C.max_ring_size)
	ringMsgMagic         = C.ring_msg_magic
	defaultReadChunkSize = datasize.ByteSize(defaultSnaplen * 32)
)

func forTestsPdumpRingWriteMsg(ring *cRingBuffer, ringData *uint8, hdr *ringMsgHdr, payload *uint8) {
	C.pdump_ring_write_msg(
		(*C.struct_ring_buffer)(ring),
		(*C.uint8_t)(ringData),
		(*C.struct_ring_msg_hdr)(hdr),
		(*C.uint8_t)(payload),
	)
}

// alignToU32 aligns offset to 4-byte boundary, matching dataplane alignment.
// Uses the same alignment as dataplane: __ALIGN4RING(val) (((val) + 3) & ~3)
// This ensures consistent alignment between writer and reader.
func alignToU32(off int) int {
	return (off + 3) & ^3
}

// ringBuffer manages multiple worker ring buffers for packet capture data.
type ringBuffer struct {
	workers       []*workerArea // Per-worker ring buffer areas
	perWorkerSize uint32        // Size of each worker's ring buffer
	// TODO: Implement configurable read chunk size.
	readChunkSize uint32 // Maximum bytes to read in one operation
}

// Clone creates a copy of the ring buffer. This is useful when multiple readers
// need their own independent ring buffer instances to avoid concurrent modification issues
// and ensure each reader has its own workerArea.readIdx.  The cloned ring buffer will
// have its own copy of the worker areas, preventing readers from interfering with each other's read positions.
// Note: The underlying ringbuffer shared memory is still shared. This copy is a shallow copy
// in terms of the shared memory ring buffer itself.  Only the worker areas are copied.
func (m *ringBuffer) Clone() *ringBuffer {
	bufferClone := *m
	workersClone := make([]*workerArea, 0, len(m.workers))
	for _, w := range m.workers {
		wCopy := *w
		workersClone = append(workersClone, &wCopy)
	}
	bufferClone.workers = workersClone
	return &bufferClone
}

// workerArea represents a single worker's ring buffer state and data.
type workerArea struct {
	writeIdx    *uint64     // Pointer to writer's current write position
	readableIdx *uint64     // Pointer to oldest readable data position
	readIdx     uint64      // Reader's current read position
	buf         []byte      // Temporary buffer for partial reads
	data        []byte      // Ring buffer data area
	mask        uint64      // Mask for efficient modulo operation
	log         *zap.Logger // Logger for this worker area
}

// spawnWakers creates notification channels for each worker and starts a background
// goroutine that periodically checks for new data and notifies waiting readers.
func (m *ringBuffer) spawnWakers(ctx context.Context) []chan bool {
	wakers := make([]chan bool, 0, len(m.workers))
	for range m.workers {
		wakers = append(wakers, make(chan bool, 1))
	}

	ticker := time.NewTicker(1 * time.Millisecond)

	go func() {
		for {
			// Check each worker for new data and notify if available
			for idx, worker := range m.workers {
				if worker.hasMore() {
					select {
					case wakers[idx] <- true: // Non-blocking notification
					default: // Skip if channel is full (reader is busy)
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return wakers
}

// runReaders starts reader goroutines for all workers and processes ring buffer data
// into protobuf records, sending them to the provided channel.
func (m *ringBuffer) runReaders(ctx context.Context, recordCh chan<- *pdumppb.Record) error {
	wakers := m.spawnWakers(ctx)
	wg, _ := errgroup.WithContext(ctx)
	for idx, worker := range m.workers {
		wg.Go(func() error {
			// Allocate buffer for accumulating partial reads across ring boundary
			worker.buf = make([]byte, 0, m.readChunkSize)

			waker := wakers[idx]
			for {
				// Read available data and convert to protobuf records
				records := worker.read(m.readChunkSize)
				for _, rec := range records {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case recordCh <- rec:
					}
				}
				// Continue immediately if more data is available
				if worker.hasMore() {
					continue
				}
				// Wait for notification of new data
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-waker:
				}
			}
		})
	}
	return wg.Wait()
}

// hasMore checks if there's unread data available in the worker's ring buffer.
func (w *workerArea) hasMore() bool {
	writeVal := atomic.LoadUint64(w.writeIdx)
	readVal := atomic.LoadUint64(&w.readIdx)
	return writeVal > readVal
}

// read extracts packet data from the ring buffer and converts it to protobuf records.
// It handles ring buffer wraparound, data overwrites, and message boundary alignment.
func (w *workerArea) read(n uint32) []*pdumppb.Record {
	// Load current ring buffer positions with acquire ordering to ensure
	// we see all writes that happened before the position updates
	readableVal := atomic.LoadUint64(w.readableIdx)
	writeVal := atomic.LoadUint64(w.writeIdx)

	// Handle case where writer has overwritten data we were reading
	// 'readableVal' currently holds the latest shared readable_idx.
	// 'w.readIdx' is our private record of how far we've successfully processed.
	if readableVal > w.readIdx {
		// Writer advanced shared readable_idx past our private w.readIdx.
		// This means any partial data in w.buf from a previous read() call is now stale
		// as it belongs to an overwritten region of the shared ring buffer.
		if len(w.buf) > 0 {
			w.buf = w.buf[:0] // Clear stale partial data
		}
		// Catch up our private read index to the new truth from shared memory.
		atomic.StoreUint64(&w.readIdx, readableVal)
	} else {
		// Shared readable_idx has not advanced past our private w.readIdx.
		// This means w.readIdx is still valid (or even ahead, though ideally not).
		// The effective starting point for this read operation will be our current w.readIdx.
		// 'readableVal' will carry this effective starting point for the rest of this function.
		readableVal = w.readIdx
	}
	// At this point, 'readableVal' is the definitive logical offset from which this read operation will attempt to fetch data.
	// And 'w.readIdx' is also at least 'readableVal' (it might be advanced further by this function if data is read).

	// Check if there's any new data to read
	if writeVal <= readableVal {
		return nil
	}

	// Calculate how much data to read, limited by chunk size to avoid
	// reading too much at once and blocking other operations
	size := min(writeVal-readableVal, uint64(n))

	// Convert logical positions to physical ring buffer indices
	readableIdx := readableVal & w.mask
	readableIdxEnd := (readableIdx + size) & w.mask

	// Remember buffer size before appending new data for overwrite detection
	beforeReadBufSize := len(w.buf)

	// Copy data from ring buffer, handling potential wraparound
	if readableIdxEnd > readableIdx {
		// Data is contiguous - simple case
		w.buf = append(w.buf, w.data[readableIdx:readableIdxEnd]...)
	} else {
		// Data wraps around ring buffer boundary - copy in two parts
		w.buf = append(w.buf, w.data[readableIdx:]...)    // From readableIdx to end
		w.buf = append(w.buf, w.data[:readableIdxEnd]...) // From start to readableIdxEnd
	}

	// Update our read position
	atomic.AddUint64(&w.readIdx, size)

	// Check if writer overwrote data while we were copying it.
	// 'readableVal' here is the 'effectiveReadStart' determined at the beginning of this function.
	latestSharedReadableIdx := atomic.LoadUint64(w.readableIdx)
	if latestSharedReadableIdx > readableVal {
		// Overwrite detected. The writer has advanced readable_idx past the point
		// from which we started reading this batch (readableVal).
		// This means any data accumulated in w.buf (both old data from
		// beforeReadBufSize and part of newly copied data) is now suspect.

		// Calculate how much data was overwritten
		diff := latestSharedReadableIdx - readableVal
		// Include any previously buffered data that's now invalid
		diff += uint64(beforeReadBufSize)

		if diff > uint64(len(w.buf)) {
			// All our data was overwritten - discard everything and retry
			w.buf = w.buf[:0]
			atomic.StoreUint64(&w.readIdx, latestSharedReadableIdx) // Reset reader position to the new valid start
			return nil
		}

		// Discard the overwritten portion by advancing buffer start.
		// We always append new data to the right and discard from the left
		// to ensure protobuf record data slices remain valid during async
		// GRPC transmission (GRPC copies data asynchronously).
		w.buf = w.buf[diff:]
	}

	response := make([]*pdumppb.Record, 0)

	// Parse complete messages from the buffer and convert to protobuf records
	for len(w.buf) >= int(cRingMsgHdrSize) {
		// Cast buffer start to message header - buffer should be aligned
		// to message boundaries from previous processing
		msgHeader := (*ringMsgHdr)(unsafe.Pointer(&w.buf[0]))

		totalLen := uint32(msgHeader.total_len)

		// Validate message header integrity
		if msgHeader.magic != ringMsgMagic || totalLen < uint32(cRingMsgHdrSize) {
			// Corrupted header detected - this should be rare with proper
			// overwrite handling, but can still occur under extreme load
			w.log.Debug("discard buffer due to magic or msg.total_len validation",
				zap.String("magic", strconv.FormatUint(uint64(msgHeader.magic), 16)),
				zap.Uint32("total_len", totalLen))
			w.buf = w.buf[:0]
			return response
		}

		// Calculate aligned skip size to find next message boundary.
		// Must match dataplane alignment to maintain message boundaries.
		skipSize := alignToU32(int(totalLen))
		if skipSize > len(w.buf) {
			// Incomplete message - wait for more data in next read
			return response
		}

		// Extract packet data (excluding header)
		data := w.buf[cRingMsgHdrSize:msgHeader.total_len]

		// Convert to protobuf record
		rec := &pdumppb.Record{
			Meta: &pdumppb.RecordMeta{
				Timestamp:   uint64(msgHeader.timestamp),
				DataSize:    uint32(len(data)),
				PacketLen:   uint32(msgHeader.packet_len),
				WorkerIdx:   uint32(msgHeader.worker_idx),
				PipelineIdx: uint32(msgHeader.pipeline_idx),
				RxDeviceId:  uint32(msgHeader.rx_device_id),
				TxDeviceId:  uint32(msgHeader.tx_device_id),
				IsDrops:     msgHeader.is_drops == 1,
			},
			Data: data,
		}
		response = append(response, rec)

		// Advance buffer past this message (including alignment padding)
		w.buf = w.buf[skipSize:]
	}

	return response
}
