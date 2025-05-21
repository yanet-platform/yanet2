package pdump

//#include "modules/pdump/api/controlplane.h"
import "C"

import (
	"context"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb"
)

const (
	minRingSize     = datasize.MB
	hdrSizeSize     = int(unsafe.Sizeof(C.struct_ring_msg_hdr{}.total_len))
	cRingMsgHdrSize = unsafe.Sizeof(C.struct_ring_msg_hdr{})
)

var (
	maxRingSize          = uint32(C.max_ring_size)
	defaultReadChunkSize = datasize.ByteSize(defaultSnaplen * 32)
)

func alignToHdrSizeSize(off int) int {
	return (off + (hdrSizeSize - 1)) & -hdrSizeSize
}

type ringBuffer struct {
	workers       []*workerArea
	perWorkerSize uint32
	// TODO: Implement configurable read chunk size.
	readChunkSize uint32
}

type workerArea struct {
	writeIdx    *uint64
	readableIdx *uint64
	readIdx     uint64
	buf         []byte
	data        []byte
	mask        uint64
}

func (m *ringBuffer) spawnWakers(ctx context.Context) []chan bool {
	wakers := make([]chan bool, 0, len(m.workers))
	for range m.workers {
		wakers = append(wakers, make(chan bool, 1))
	}

	ticker := time.NewTicker(1 * time.Millisecond)

	go func() {
		for {
			for idx, worker := range m.workers {
				if worker.hasMore() {
					select {
					case wakers[idx] <- true:
					default:
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

func (m *ringBuffer) runReaders(ctx context.Context, recordCh chan<- *pdumppb.Record) error {
	wakers := m.spawnWakers(ctx)
	wg, _ := errgroup.WithContext(ctx)
	for idx, worker := range m.workers {
		wg.Go(func() error {
			// Allocate an internal buffer to store intermediate partial read results.
			worker.buf = make([]byte, 0, m.readChunkSize)

			waker := wakers[idx]
			for {
				records := worker.read(m.readChunkSize)
				for _, rec := range records {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case recordCh <- rec:
					}
				}
				if worker.hasMore() {
					continue
				}
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

func (w *workerArea) hasMore() bool {
	writeVal := atomic.LoadUint64(w.writeIdx)
	readVal := atomic.LoadUint64(&w.readIdx)
	return writeVal > readVal
}

func (w *workerArea) read(n uint32) []*pdumppb.Record {
	// Get the read counter value
	readableVal := atomic.LoadUint64(w.readableIdx)
	// Get the write counter value
	writeVal := atomic.LoadUint64(w.writeIdx)

	if readableVal > w.readIdx {
		// If the writer has overwritten some of the readable data,
		// continue from the new readable index.
		atomic.StoreUint64(&w.readIdx, readableVal)
	} else {
		// If we're still reading the readable portion and the writer hasn't
		// overwritten it, continue reading from readIdx.
		readableVal = w.readIdx
	}

	if writeVal <= readableVal {
		// If no writes have occurred since the last read, return.
		return nil
	}

	// Calculate the readable part size.
	// Limit reading to a chunk size (the free space in the buffer with a length of readChunkSize).
	size := min(writeVal-readableVal, uint64(n))

	// Calculate indices within the worker's ring data.
	readableIdx := readableVal & w.mask
	readableIdxEnd := (readableIdx + size) & w.mask

	beforeReadBufSize := len(w.buf)
	if readableIdxEnd > readableIdx {
		// If the write index is past the read index, there's a contiguous chunk of data.
		w.buf = append(w.buf, w.data[readableIdx:readableIdxEnd]...)
	} else {
		// If the write index is before the read index, the readable data spans
		// across the end and beginning of the buffer.
		w.buf = append(w.buf, w.data[readableIdx:]...)
		w.buf = append(w.buf, w.data[:readableIdxEnd]...)
	}
	atomic.AddUint64(&w.readIdx, size)

	// Check if the ring worker has overwritten data during the read process;
	// if so, discard the overwritten data.
	newReadableVal := atomic.LoadUint64(w.readableIdx)
	diff := int(newReadableVal - readableVal)
	if diff > 0 {
		// If the ring worker has overwritten data, any previously partially
		// read data is now invalid and should be discarded.
		diff += beforeReadBufSize
		// If a worker has overwritten past the end of the last reading,
		// all read data is invalid; return nothing to try again on the next iteration.
		if diff > len(w.buf) {
			w.buf = w.buf[:0]
			return nil
		}
		// Forget overwritten data by shifting the beginning of the slice to
		// the data after the overwritten part.
		// We intentionally always allocate from the right and discard data
		// from the left. This is because, during conversion to the proto
		// Records, the data passed to those records should not be modified by
		// us. GRPC will copy the data for network transmission asynchronously,
		// and any modifications we make could potentially corrupt the GRPC
		// message.
		w.buf = w.buf[diff:]

	}
	response := make([]*pdumppb.Record, 0)

	// While there is sufficient data for the size of the message size,
	// we'll attempt to convert the data to a proto Record.
	for len(w.buf) > int(cRingMsgHdrSize) {
		// Now the beginning of the buffer should point to the header of the ring
		// buffer message.
		// An important detail is that this size doesn't include data alignment
		// to a u32 boundary; therefore, we must skip this alignment if it exists
		// to reach the next message header.
		msgHeader := (*C.struct_ring_msg_hdr)(unsafe.Pointer(&w.buf[0]))
		// We must always read data with alignment padding. Failure to do so
		// can prevent proper handling of subsequent alignment padding,
		// effectively losing information about the next message header's location.
		skipSize := alignToHdrSizeSize(int(msgHeader.total_len))
		if skipSize > len(w.buf) {
			// If there isn't enough data for the entire message (including
			// alignment), we'll try to read more on the next attempt.
			return response
		}
		data := w.buf[cRingMsgHdrSize:msgHeader.total_len]

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

		w.buf = w.buf[skipSize:]
	}

	return response
}
