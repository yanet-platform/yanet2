package cring

//#include "common/ring.h"
import "C"

import (
	"encoding/binary"
	"fmt"
	"slices"
	"sync/atomic"
)

// RecordFrameSize is the size of the frame in front of every record's
// payload.
//
// It is also the smallest record length a ring accepts.
const RecordFrameSize = uint32(C.RING_RECORD_FRAME_SIZE)

// Record is one payload read from a worker's ring.
//
// It carries the worker index and the sequence number the writer wrote into
// the record's frame. The records of one read share one allocation, and nothing else
// uses it. So a later read does not change the payload. The payload's
// capacity equals its length, so an append allocates new memory and never
// writes into the next record.
type Record struct {
	Worker uint16
	Seqno  uint32
	Bytes  []byte
}

// RecordSource gives a Reader raw access to one ring.
//
// It returns the two shared positions and copies bytes out of the data
// area. A ring object provides the real source in shared memory. A caller
// may pass its own source to run the read protocol against writer state
// that the caller controls.
type RecordSource interface {
	// Indices returns the current logical write and readable positions, in
	// that order.
	Indices() (write, readable uint64)
	// CopyRange copies the requested number of bytes from a logical offset
	// of the data area.
	//
	// The destination is exactly as long as the request. The copy wraps at
	// the physical end of the data area.
	CopyRange(dst []byte, start, size uint64)
}

// shmSource is the RecordSource of one worker's ring in shared memory.
type shmSource struct {
	writeIdx    *uint64
	readableIdx *uint64
	data        []byte
	mask        uint64
}

func (m *shmSource) Indices() (write, readable uint64) {
	return atomic.LoadUint64(m.writeIdx), atomic.LoadUint64(m.readableIdx)
}

func (m *shmSource) CopyRange(dst []byte, start, size uint64) {
	if size == 0 {
		return
	}

	startPos := start & m.mask
	endPos := (startPos + size) & m.mask
	if endPos > startPos {
		copy(dst, m.data[startPos:startPos+size])
		return
	}

	n := copy(dst, m.data[startPos:])
	copy(dst[n:], m.data[:endPos])
}

// Reader reads published records from one worker's ring.
//
// Each reader has its own read position. Other readers of the same worker
// do not affect it. Only one goroutine may call Read at a time. Another
// goroutine may call HasMore while Read runs.
type Reader struct {
	worker   uint16
	capacity uint32
	src      RecordSource

	readIdx atomic.Uint64
	// buf is a scratch buffer reused across calls. No caller ever gets it.
	//
	// Between calls it holds only the start of a record that is not yet
	// fully copied. That start sits at the front of the buffer.
	buf []byte
}

// NewReader creates a Reader for one worker's ring.
//
// The reader starts at the oldest readable record and tags every record
// with the worker index. It treats a record length above the ring
// capacity as corruption. NewReader fails for a capacity below the frame
// size: no record fits such a ring, so every length would look corrupt.
func NewReader(worker uint16, capacity uint32, src RecordSource) (*Reader, error) {
	if capacity < RecordFrameSize {
		return nil, fmt.Errorf("ring capacity %d is below the record frame size %d", capacity, RecordFrameSize)
	}
	return &Reader{worker: worker, capacity: capacity, src: src}, nil
}

// NewReaderFromTail creates a Reader like NewReader, except that it
// starts at the source's current write position instead of the oldest
// readable record.
//
// It skips every record already committed; only a record written after
// this call reaches it. A caller that wants a fresh stream to see only
// new traffic, not a ring's history, uses this instead of NewReader.
func NewReaderFromTail(worker uint16, capacity uint32, src RecordSource) (*Reader, error) {
	reader, err := NewReader(worker, capacity, src)
	if err != nil {
		return nil, err
	}

	write, _ := src.Indices()
	reader.readIdx.Store(write)
	return reader, nil
}

// HasMore reports whether the ring has data this Reader has not read yet.
func (m *Reader) HasMore() bool {
	write, _ := m.src.Indices()
	return write > m.readIdx.Load()
}

// Read copies up to the given number of new bytes and returns the whole
// records in them.
//
// After the copy, Read moves its position forward and loads the readable
// position again. If the writer evicted bytes during the copy, Read drops
// them, so the caller never gets an overwritten record. A record length
// outside [frame size, capacity] is corruption. Read then returns the
// records before it, drops the rest of the buffer, and resumes at the write
// position it loaded at the start. A normal call allocates one slice and
// one payload block, and nothing when no record is complete.
func (m *Reader) Read(maxBytes uint32) []Record {
	write, readable := m.src.Indices()

	if readable > m.readIdx.Load() {
		// The writer evicted data this reader had not read yet. A partial
		// record kept from the previous call is now invalid.
		m.buf = m.buf[:0]
		m.readIdx.Store(readable)
	} else {
		readable = m.readIdx.Load()
	}

	// All bytes below the published write position belong to whole
	// records that are fully written.
	if write <= readable {
		return nil
	}

	size := min(write-readable, uint64(maxBytes))

	before := len(m.buf)
	after := before + int(size)
	m.buf = slices.Grow(m.buf, int(size))[:after]
	m.src.CopyRange(m.buf[before:after], readable, size)

	// Keep this add and the reload below atomic and in this order.
	//
	// The writer moves the readable position forward before it overwrites
	// bytes. So the copy's loads must finish before Read loads the position
	// again. On arm64 the add is a release, so the copy stays above it. The
	// reload is an acquire, so it cannot move above the add. The reload then
	// sees every eviction whose new bytes the copy saw. The add also
	// publishes the read position that HasMore checks from another goroutine.
	m.readIdx.Add(size)

	_, latest := m.src.Indices()
	if latest > readable {
		diff := latest - readable + uint64(before)
		if diff > uint64(len(m.buf)) {
			// The writer evicted all buffered bytes. Drop them and resume at
			// the new readable position.
			m.buf = m.buf[:0]
			m.readIdx.Store(latest)
			return nil
		}
		m.dropPrefix(int(diff))
	}

	// First pass: count whole records and their payload bytes. The second
	// pass then allocates the slice and the payload block once each.
	parsed := 0
	count := 0
	payloadBytes := 0
	corrupt := false
	for len(m.buf)-parsed >= int(RecordFrameSize) {
		totalLen := binary.LittleEndian.Uint32(m.buf[parsed : parsed+4])
		if totalLen < RecordFrameSize || totalLen > m.capacity {
			corrupt = true
			break
		}
		skip := int(align4(totalLen))
		if skip > len(m.buf)-parsed {
			break
		}
		count++
		payloadBytes += int(totalLen - RecordFrameSize)
		parsed += skip
	}

	var records []Record
	if count > 0 {
		records = make([]Record, 0, count)
		payloads := make([]byte, payloadBytes)
		for offset := 0; offset < parsed; {
			totalLen := binary.LittleEndian.Uint32(m.buf[offset : offset+4])
			seqno := binary.LittleEndian.Uint32(m.buf[offset+4 : offset+8])

			n := copy(payloads, m.buf[offset+int(RecordFrameSize):offset+int(totalLen)])
			records = append(records, Record{
				Worker: m.worker,
				Seqno:  seqno,
				Bytes:  payloads[:n:n],
			})
			payloads = payloads[n:]
			offset += int(align4(totalLen))
		}
	}

	if corrupt {
		// Drop the rest of the buffer and resume at the write position loaded
		// at the start.
		//
		// The writer moves that position only by whole committed records. So
		// the next read starts at a real frame and does not read payload bytes
		// as a frame.
		m.buf = m.buf[:0]
		m.readIdx.Store(write)
		return records
	}

	m.dropPrefix(parsed)
	return records
}

// dropPrefix drops the given number of buffered bytes and moves the rest to the
// front, so the buffer memory is reused.
//
// This is safe because no returned record points into the buffer.
func (m *Reader) dropPrefix(n int) {
	m.buf = m.buf[:copy(m.buf, m.buf[n:])]
}

// align4 rounds a record length up to the 4-byte boundary every record
// starts at.
//
// It wraps like the C writer's 32-bit arithmetic.
func align4(totalLen uint32) uint32 {
	return (totalLen + 3) &^ 3
}
