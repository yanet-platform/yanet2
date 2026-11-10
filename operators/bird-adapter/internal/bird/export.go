package bird

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
)

type Updater func(context.Context, []rib.Route) error

// Notifier is called after each dumped batch, with the same context the
// batch was dumped with.
type Notifier func(context.Context) error

// Dialer connects to the BIRD export socket at path.
type Dialer func(ctx context.Context, path string) (net.Conn, error)

type exportSocket struct {
	Path    string
	BufSize int
}

type Export struct {
	sockets  []exportSocket
	cfg      *Config
	updater  Updater
	notifier Notifier
	onIdle   func(context.Context)
	// idleInterval is how often the idle callback runs, the dump timeout
	// when zero.
	idleInterval time.Duration
	dial         Dialer
	log          *zap.Logger
}

// ExportReaderOption configures the Export constructor.
type ExportReaderOption func(*exportReaderOptions)

type exportReaderOptions struct {
	OnIdle       func(context.Context)
	IdleInterval time.Duration
	Dial         Dialer
	Log          *zap.Logger
}

func newExportReaderOptions() *exportReaderOptions {
	return &exportReaderOptions{
		OnIdle: func(context.Context) {},
		Dial:   dialUnix,
		Log:    zap.NewNop(),
	}
}

// dialUnix connects to a Unix socket.
func dialUnix(ctx context.Context, path string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", path)
}

// WithExportReaderIdle sets the callback the reader runs on every idle tick,
// whether or not routes wait for their dump, so the consumer can retry what
// an earlier update failed to deliver without waiting for new routes.
func WithExportReaderIdle(onIdle func(context.Context)) ExportReaderOption {
	return func(o *exportReaderOptions) {
		o.OnIdle = onIdle
	}
}

// WithExportReaderIdleInterval sets how often the idle callback runs, the
// dump timeout by default.
func WithExportReaderIdleInterval(interval time.Duration) ExportReaderOption {
	return func(o *exportReaderOptions) {
		o.IdleInterval = interval
	}
}

// WithExportReaderDialer sets the function that connects to every export
// socket, a Unix socket dialer by default.
//
// Another dialer lets the reader run over an in-memory transport.
func WithExportReaderDialer(dial Dialer) ExportReaderOption {
	return func(o *exportReaderOptions) {
		o.Dial = dial
	}
}

// WithExportReaderLog sets the logger for the export reader.
func WithExportReaderLog(log *zap.Logger) ExportReaderOption {
	return func(o *exportReaderOptions) {
		o.Log = log
	}
}

func NewExportReader(cfg *Config, onUpdate Updater, onFlush Notifier, options ...ExportReaderOption) *Export {
	opts := newExportReaderOptions()
	for _, o := range options {
		o(opts)
	}

	sockets := make([]exportSocket, 0, len(cfg.Sockets))
	for _, s := range cfg.Sockets {
		sockets = append(sockets, exportSocket{
			Path:    s,
			BufSize: int(cfg.ParserBufSize.Bytes()),
		})
	}
	return &Export{
		sockets:      sockets,
		cfg:          cfg,
		updater:      onUpdate,
		notifier:     onFlush,
		onIdle:       opts.OnIdle,
		idleInterval: opts.IdleInterval,
		dial:         opts.Dial,
		log:          opts.Log,
	}
}

func (m *Export) Run(ctx context.Context) error {
	if len(m.cfg.Sockets) == 0 {
		m.log.Info("bird export reader is disabled, no sockets provided")
		return nil
	}

	// Any capacity above zero works; a small one keeps parsed routes from
	// piling up in memory when the batch processor falls behind.
	updates := make(chan *rib.Route, 10)
	defer close(updates)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	m.log.Info("starting socket readers for bird export")
	wg, ctx := errgroup.WithContext(ctx)
	for _, socket := range m.sockets {
		wg.Go(func() error {
			m.log.Info("starting bird export reader",
				zap.String("path", socket.Path))

			c, err := m.dial(ctx, socket.Path)
			if err != nil {
				return fmt.Errorf("failed to dial bird export socket '%s': %w", socket.Path, err)
			}
			// Closing the socket on cancellation unblocks the read below; the
			// reader waits for that close, so it leaves no goroutine behind.
			closed := make(chan struct{})
			defer func() {
				cancel(nil)
				<-closed
			}()
			go func() {
				defer close(closed)
				<-ctx.Done()
				if err := c.Close(); err != nil {
					m.log.Warn("bird socket closed with an error",
						zap.Error(err),
						zap.NamedError("cause", context.Cause(ctx)),
					)
				}
			}()

			reader := bufio.NewReader(c)
			parser := NewParser(reader, socket.BufSize, WithParserLog(m.log))
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
				route := &rib.Route{}
				if err := update.Decode(route); err != nil {
					cancel(err)
					return fmt.Errorf("failed to decode next route update: %w", err)
				}
				route.SourceID = rib.RouteSourceBird

				select {
				case <-ctx.Done():
					return context.Cause(ctx)
				case updates <- route:
				}
			}
		})

	}

	wg.Go(func() error {
		m.log.Info("starting batch processor for bird route updates")
		batch := make([]rib.Route, 0, m.cfg.DumpThreshold)
		tick := time.NewTicker(m.cfg.DumpTimeout)
		defer tick.Stop()
		idleInterval := m.idleInterval
		if idleInterval <= 0 {
			idleInterval = m.cfg.DumpTimeout
		}
		idle := time.NewTicker(idleInterval)
		defer idle.Stop()
		timeout := false
		for {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case route := <-updates:
				batch = append(batch, *route)
				// A batch waits at most one timeout after its first route:
				// routes that follow join it without postponing the dump.
				if len(batch) == 1 {
					tick.Reset(m.cfg.DumpTimeout)
				}
			case <-tick.C:
				if len(batch) == 0 {
					continue
				}
				timeout = true
			case <-idle.C:
				m.onIdle(ctx)
				continue
			}

			if timeout || len(batch) >= m.cfg.DumpThreshold {
				m.log.Debug("send RIB update", zap.Int("size", len(batch)),
					zap.Bool("isTimeout", timeout))
				if err := m.updater(ctx, batch); err != nil {
					return fmt.Errorf("failed to call updater: %w", err)
				}
				batch = batch[:0]

				if err := m.notifier(ctx); err != nil {
					return fmt.Errorf("failed to call notifier: %w", err)
				}
			}

			timeout = false
		}
	})

	err := wg.Wait()
	m.log.Info("export readers are stopped", zap.Error(err))
	return err
}
