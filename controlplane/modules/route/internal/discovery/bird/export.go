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
	if !m.cfg.Enabled {
		m.log.Info("Bird export reader is disabled")
		return nil
	}

	updates := make(chan *rib.Route, 100)
	defer close(updates)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	m.log.Info("Starting socket readers for bird export")
	wg, ctx := errgroup.WithContext(ctx)
	for _, socket := range m.sockets {
		wg.Go(func() error {
			m.log.Infow("Starting bird export reader",
				zap.String("name", socket.name),
				zap.String("path", socket.path))

			c, err := net.Dial("unix", socket.path)
			if err != nil {
				return fmt.Errorf("net.Dial(unix,  %s): %w", socket.path, err)
			}
			go func() {
				<-ctx.Done()
				_ = c.Close()
			}()
			reader := bufio.NewReader(c)
			parser := NewParser(reader, socket.bufSize, m.log)
			for {
				update, err := parser.Next()
				if err != nil {
					cancel(err)
					return fmt.Errorf("bird export parser.Next: %w", err)
				}
				route := rib.MakeRoute()
				if err := update.Decode(route); err != nil {
					cancel(err)
					return fmt.Errorf("update.Decode(): %w", err)
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
		m.log.Info("Starting batch processor for bird route updates")
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

			m.log.Debugw("Send RIB update", zap.Int("size", len(batch)),
				zap.Bool("isTimeout", timeout))
			if err := m.updater.BulkUpdate(batch); err != nil {
				return fmt.Errorf("RIBUpdater.BulkUpdate: %w", err)
			}
			batch = batch[:0]
		}
	})
	m.log.Infow("Wait for export readers completion")
	return wg.Wait()
}
