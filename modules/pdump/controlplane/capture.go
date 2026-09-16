package pdump

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
)

// recordBufferSize bounds how far a stream's readers run ahead of its
// client.
const recordBufferSize = 16

// errRetired is reported by a capture that was retired before a stream
// started, so the caller can look up the capture that replaced it.
var errRetired = errors.New("capture retired")

// capture is the configuration published under one name together with the
// streams reading the rings of its module.
//
// The rings live in the module's memory, so a stream must stop reading them
// before the module is released. Free is what enforces that: it ends the
// streams and waits for them before it releases the module.
type capture struct {
	settings Settings
	module   Module
	readers  *gate
	log      *zap.Logger
}

// Settings returns the capture parameters the config was published with.
func (m *capture) Settings() Settings {
	return m.settings
}

// Read streams the captured records until the stream ends, the client
// cannot take them any more or the capture is retired.
//
// A capture retired before the stream started reports errRetired and reads
// nothing.
func (m *capture) Read(ctx context.Context, send func(*pdumppb.Record) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	records := make(chan *pdumppb.Record, recordBufferSize)
	result := make(chan error, 1)
	go func() {
		defer close(records)

		var readErr error
		// The rings are taken inside the gate, so a retired capture is
		// never read at all.
		if !m.readers.Run(ctx, func(ctx context.Context) {
			rings := m.module.Rings()
			if len(rings) == 0 {
				readErr = fmt.Errorf("config is not initialized properly")
				return
			}

			ring := &ringBuffer{
				workers:       workerAreas(rings, m.log),
				PerWorkerSize: m.settings.RingSize,
				ReadChunkSize: uint32(defaultReadChunkSize.Bytes()),
			}
			m.log.Info("start ring readers", zap.Int("count", ring.WorkerCount()))
			info := ring.RunReaders(ctx, records)
			m.log.Info("ring readers stopped", zap.Any("info", info))
		}) {
			readErr = errRetired
		}
		result <- readErr
	}()

	// Sending outside the gate keeps a slow client from delaying a retire.
	for record := range records {
		if err := send(record); err != nil {
			cancel()
			for range records {
			}
			return err
		}
	}

	return <-result
}

// Free ends the streams, waits until none of them reads the rings, then
// releases the module.
func (m *capture) Free() error {
	m.readers.Close()
	return m.module.Free()
}

// gate admits work until it closes, and close waits for the work it
// admitted.
type gate struct {
	mu     sync.Mutex
	closed bool
	work   sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func newGate() *gate {
	ctx, cancel := context.WithCancel(context.Background())
	return &gate{ctx: ctx, cancel: cancel}
}

// Run runs work unless the gate is closed, cancelling its context once the
// gate closes.
//
// It reports whether the work ran at all.
func (m *gate) Run(ctx context.Context, work func(context.Context)) bool {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	m.work.Add(1)
	m.mu.Unlock()
	defer m.work.Done()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(m.ctx, cancel)()

	work(ctx)
	return true
}

// Close refuses further work, cancels the running work and waits for it.
//
// Calling it again returns at once.
func (m *gate) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	m.cancel()
	m.work.Wait()
}
