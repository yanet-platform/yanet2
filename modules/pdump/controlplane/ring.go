package pdump

import (
	"context"
	"encoding/binary"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

// readChunkBytes bounds how many bytes a single read pulls from one
// worker's ring before its records are sent on.
//
// So one very busy worker cannot starve the others for long.
var readChunkBytes = defaultSnaplen * 32

// wakerStartInterval is the waker's poll interval right after any reader
// found data, and what a poll that finds data resets it to.
const wakerStartInterval = 100 * time.Microsecond

// wakerMaxInterval caps the exponential backoff an empty poll grows the
// waker's interval to, so an idle capture polls at most this often.
const wakerMaxInterval = 10 * time.Millisecond

// parseRecordMeta decodes the 32-byte pdump metadata block
// (modules/pdump/dataplane/record.h) at the front of a record's payload.
//
// It reports false when the payload is shorter than the block or the
// block's magic does not match, so a caller drops the record instead of
// trusting bytes that are not really metadata. The returned data is the
// payload bytes behind the block.
func parseRecordMeta(payload []byte) (meta *pdumppb.RecordMeta, data []byte, ok bool) {
	if uint64(len(payload)) < uint64(pdumpRecordHdrSize) {
		return nil, nil, false
	}
	if binary.LittleEndian.Uint32(payload[0:4]) != pdumpRecordMagic {
		return nil, nil, false
	}

	data = payload[pdumpRecordHdrSize:]
	meta = &pdumppb.RecordMeta{
		PacketLen:   binary.LittleEndian.Uint32(payload[4:8]),
		Timestamp:   binary.LittleEndian.Uint64(payload[8:16]),
		WorkerIdx:   binary.LittleEndian.Uint32(payload[16:20]),
		PipelineIdx: binary.LittleEndian.Uint32(payload[20:24]),
		RxDeviceId:  uint32(binary.LittleEndian.Uint16(payload[24:26])),
		TxDeviceId:  uint32(binary.LittleEndian.Uint16(payload[26:28])),
		Queue:       uint32(payload[28]),
		DataSize:    uint32(len(data)),
	}
	return meta, data, true
}

// runReaders opens one reader per source and reads them until the
// context is done, parsing whole records and sending each to the
// record channel.
//
// Each reader starts at its source's current write position, so a
// session sees only records committed after it started, never the
// ring's history. Each source gets its own reader and its own cursor, so
// one reader never affects another's. A record with a short or
// mismatched header is dropped, not treated as fatal, since the writer's
// own alignment keeps later records on frame boundaries; a session logs
// only its first such drop, plus a total when it stops, instead of one
// line per record.
func runReaders(
	ctx context.Context,
	sources []cring.RecordSource,
	capacity uint32,
	log *zap.Logger,
	recordCh chan<- *pdumppb.Record,
) error {
	readers := make([]*cring.Reader, 0, len(sources))
	for idx, src := range sources {
		reader, err := cring.NewReaderFromTail(uint16(idx), capacity, src)
		if err != nil {
			return err
		}
		readers = append(readers, reader)
	}

	wakers := newWakers(len(readers))
	group, ctx := errgroup.WithContext(ctx)
	// The waker reads the rings too, so it runs in the same group and the
	// session does not return before it stops.
	group.Go(func() error {
		runWaker(ctx, readers, wakers)
		return nil
	})
	for idx, reader := range readers {
		waker := wakers[idx]
		group.Go(func() error {
			var malformed int
			defer func() {
				if malformed > 0 {
					log.Warn("dropped malformed pdump records in this session",
						zap.Int("worker", idx), zap.Int("count", malformed))
				}
			}()

			for {
				for _, rec := range reader.Read(readChunkBytes) {
					meta, data, ok := parseRecordMeta(rec.Bytes)
					if !ok {
						if malformed == 0 {
							log.Warn("dropped the first malformed pdump record in this session",
								zap.Int("worker", idx), zap.Int("len", len(rec.Bytes)))
						}
						malformed++
						continue
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case recordCh <- &pdumppb.Record{Meta: meta, Data: data}:
					}
				}
				if reader.HasMore() {
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
	return group.Wait()
}

// newWakers returns one wake-up channel per reader, each holding at most
// one pending wake-up.
func newWakers(count int) []chan struct{} {
	wakers := make([]chan struct{}, count)
	for idx := range wakers {
		wakers[idx] = make(chan struct{}, 1)
	}
	return wakers
}

// runWaker polls every reader's source for new data and notifies the
// matching channel until the context ends.
//
// It polls at the start interval right after a poll actually wakes an
// idle reader, and doubles its interval otherwise, up to the max interval.
// A reader that already has an unconsumed wake-up pending does not reset
// the interval: it is busy, not idle, and unread data sitting behind a
// slow client must not keep the waker spinning at its fastest rate
// forever. One timer serves every poll, so a busy capture does not
// allocate a timer per poll.
func runWaker(ctx context.Context, readers []*cring.Reader, wakers []chan struct{}) {
	interval := wakerStartInterval
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		woke := false
		for idx, reader := range readers {
			if !reader.HasMore() {
				continue
			}
			select {
			case wakers[idx] <- struct{}{}:
				woke = true
			default: // the reader has not drained the last wake-up yet
			}
		}

		if woke {
			interval = wakerStartInterval
		} else {
			interval *= 2
			if interval > wakerMaxInterval {
				interval = wakerMaxInterval
			}
		}

		timer.Reset(interval)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
