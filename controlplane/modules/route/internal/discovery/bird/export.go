package bird

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/rib"
)

type RIBUpdater interface {
	BulkUpdate([]*rib.Route) error
}

type exportSocket struct {
	name    string
	path    string
	bufSize int
}

type Export struct {
	sockets []exportSocket
	ch      chan []rib.Route
	cfg     *Config
	updater RIBUpdater
	log     *zap.SugaredLogger
}

func NewExportReader(cfg *Config, ribUpdater RIBUpdater, log *zap.SugaredLogger) *Export {
	sockets := make([]exportSocket, 0, len(cfg.Sockets))
	for _, s := range cfg.Sockets {
		sockets = append(sockets, exportSocket{
			name:    s.Name,
			path:    s.Path,
			bufSize: int(cfg.ParserBufSize.Bytes()),
		})
	}
	return &Export{
		sockets: sockets,
		cfg:     cfg,
		updater: ribUpdater,
		log:     log,
	}
}

func (m *Export) Run(ctx context.Context) error {
	if !m.cfg.Enable {
		m.log.Info("bird export reader is disabled")
		return nil
	}

	// Any value greater then zero will be sufficient for the channel capacity.
	// A buffered channel will reduce concurrency pressure, but it seems that
	// the reading part can easily keep up with the parsing part.
	// On the other hand, if RIBUpdater.BulkUpdate cannot catch up with the
	// parser's speed, there is no reason to hold too many routes in memory.
	updates := make(chan *rib.Route, 10)
	defer close(updates)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	m.log.Info("starting socket readers for bird export")
	wg, ctx := errgroup.WithContext(ctx)
	for _, socket := range m.sockets {
		wg.Go(func() error {
			m.log.Infow("starting bird export reader",
				zap.String("name", socket.name),
				zap.String("path", socket.path))

			c, err := net.Dial("unix", socket.path)
			if err != nil {
				return fmt.Errorf("failed to dial bird export socket '%s': %w", socket.path, err)
			}
			go func() {
				<-ctx.Done()
				if err := c.Close(); err != nil {
					m.log.Warnw("bird socket closed with an error", zap.Error(err))
				}
			}()
			reader := bufio.NewReader(c)
			parser := NewParser(reader, socket.bufSize, m.log)
			for {
				update, err := parser.Next()
				if err != nil {
					if err == ErrUnsupportedRDType {
						// FIXME add telemetry
						continue
					}
					cancel(err)
					return fmt.Errorf("failed to parse next update chunk: %w", err)
				}
				route := rib.MakeBirdRoute()
				if err := update.Decode(route); err != nil {
					cancel(err)
					return fmt.Errorf("failed to decode next route update: %w", err)
				}

				select {
				case <-ctx.Done():
					return ctx.Err()
				case updates <- route:
				}
			}
		})

	}

	wg.Go(func() error {
		m.log.Info("starting batch processor for bird route updates")
		batch := make([]*rib.Route, 0, m.cfg.DumpThreshold)
		tick := time.NewTimer(m.cfg.DumpTimeout)
		timeout := false
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case route := <-updates:
				batch = append(batch, route)
			case <-tick.C:
				timeout = true
			}
			if timeout {
				tick.Reset(m.cfg.DumpTimeout)
				if len(batch) == 0 {
					continue
				}
			} else if len(batch)-1 < m.cfg.DumpThreshold {
				continue
			}

			m.log.Debugw("send RIB update", zap.Int("size", len(batch)),
				zap.Bool("isTimeout", timeout))
			if err := m.updater.BulkUpdate(batch); err != nil {
				return fmt.Errorf("failed to call rib bulk update: %w", err)
			}
			batch = batch[:0]
		}
	})
	m.log.Infow("Wait for export readers completion")
	return wg.Wait()
}
