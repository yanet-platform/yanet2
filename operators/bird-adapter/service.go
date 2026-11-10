package bird_adapter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"google.golang.org/grpc"
	grpcbackoff "google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	"github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
	adapterpb "github.com/yanet-platform/yanet2/operators/bird-adapter/adapterpb/v1"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/bird"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/mpls"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
	routepb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const (
	// importRetryMaxDelay caps every wait before a new import attempt and
	// every reconnect of the connection, after the jitter.
	importRetryMaxDelay = 30 * time.Second
	// importHeartbeatInterval is how long a stream stays silent before an
	// idle tick sends a heartbeat on it, whatever the dump timeout.
	importHeartbeatInterval = 5 * time.Second
	// importHealthyUptime is how long an attempt must stay up for the retry
	// delay to start over from its initial value.
	importHealthyUptime = 10 * time.Minute

	// routeOperatorKeepaliveTime stays above the five minutes a gRPC server
	// tolerates between pings by default, so the gateway never answers the
	// pings with a too-many-pings shutdown.
	//
	// The pings are only a fallback. gRPC also applies the timeout as the TCP
	// user timeout, so the kernel drops a half-open connection about that
	// long after a heartbeat stays unacknowledged.
	routeOperatorKeepaliveTime    = 5*time.Minute + 30*time.Second
	routeOperatorKeepaliveTimeout = 20 * time.Second
	// routeOperatorMinConnectTimeout is the gRPC default, which a custom
	// reconnect backoff would otherwise reset to zero.
	routeOperatorMinConnectTimeout = 20 * time.Second
)

// errServiceClosed rejects an import set up after the service was closed.
var errServiceClosed = errors.New("adapter service is closed")

// errFeedRIBClosedByServer ends an attempt whose stream the route operator
// closed with a summary.
var errFeedRIBClosedByServer = errors.New("route operator closed the FeedRIB stream")

// levelFilterCore wraps a zapcore.Core and filters log entries by level.
// It replaces the base core's level check with its own.
type levelFilterCore struct {
	zapcore.Core
	level zapcore.Level
}

func (m *levelFilterCore) Enabled(lvl zapcore.Level) bool {
	return m.level.Enabled(lvl)
}

func (m *levelFilterCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if m.Enabled(ent.Level) {
		return ce.AddCore(ent, m)
	}
	return ce
}

// AdapterService implements the Adapter gRPC service for the route module.
type AdapterService struct {
	adapterpb.UnimplementedAdapterServiceServer

	// ctx parents every import and every setup in progress, and cancel
	// stops them all on close.
	ctx    context.Context
	cancel context.CancelFunc

	importsMu sync.Mutex
	imports   map[string]*importHolder
	// closed refuses new setups once the service is closing.
	closed bool
	// inflight counts the setups in progress and the running imports.
	//
	// A setup joins it under the mutex before its first blocking call and
	// only while the service is open, so nothing joins after the close
	// started waiting.
	inflight sync.WaitGroup
	// stopped is closed once every setup and import has exited after close.
	stopped chan struct{}

	routeOperatorEndpoint    string                           // gRPC endpoint of the route operator's RouteService for RIB updates
	routeOperatorCredentials credentials.TransportCredentials // Transport credentials for that endpoint
	// routeOperatorDialOptions follow the adapter's own options for that
	// endpoint, so they override them, and precede the credentials.
	routeOperatorDialOptions []grpc.DialOption
	// birdDialer connects to the BIRD export sockets in place of the Unix
	// socket dialer when set.
	birdDialer bird.Dialer
	log        *zap.Logger
}

// AdapterServiceOption configures the AdapterService constructor.
type AdapterServiceOption func(*adapterServiceOptions)

type adapterServiceOptions struct {
	RouteOperatorCredentials credentials.TransportCredentials
	RouteOperatorDialOptions []grpc.DialOption
	BIRDDialer               bird.Dialer
	Log                      *zap.Logger
}

func newAdapterServiceOptions() *adapterServiceOptions {
	return &adapterServiceOptions{
		RouteOperatorCredentials: insecure.NewCredentials(),
		Log:                      zap.NewNop(),
	}
}

// WithAdapterServiceLog sets the logger for the adapter service.
func WithAdapterServiceLog(log *zap.Logger) AdapterServiceOption {
	return func(o *adapterServiceOptions) {
		o.Log = log
	}
}

// WithRouteOperatorCredentials sets the transport credentials for the route
// operator connection, plaintext by default.
func WithRouteOperatorCredentials(creds credentials.TransportCredentials) AdapterServiceOption {
	return func(o *adapterServiceOptions) {
		o.RouteOperatorCredentials = creds
	}
}

// WithRouteOperatorDialOptions adds gRPC dial options to the route operator
// connection, applied after the adapter's own ones so they override them,
// except the transport credentials, which they cannot replace.
//
// A context dialer lets the connection run over an in-memory transport;
// the endpoint then needs a scheme that skips name resolution, such as
// passthrough.
func WithRouteOperatorDialOptions(options ...grpc.DialOption) AdapterServiceOption {
	return func(o *adapterServiceOptions) {
		o.RouteOperatorDialOptions = append(o.RouteOperatorDialOptions, options...)
	}
}

// WithBIRDDialer sets the function that connects to the BIRD export sockets,
// a Unix socket dialer by default.
//
// Another dialer lets the imports read BIRD over an in-memory transport.
func WithBIRDDialer(dial func(ctx context.Context, path string) (net.Conn, error)) AdapterServiceOption {
	return func(o *adapterServiceOptions) {
		o.BIRDDialer = dial
	}
}

func NewAdapterService(
	routeOperatorEndpoint string,
	options ...AdapterServiceOption,
) *AdapterService {
	opts := newAdapterServiceOptions()
	for _, o := range options {
		o(opts)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &AdapterService{
		ctx:                      ctx,
		cancel:                   cancel,
		imports:                  map[string]*importHolder{},
		stopped:                  make(chan struct{}),
		routeOperatorEndpoint:    routeOperatorEndpoint,
		routeOperatorCredentials: opts.RouteOperatorCredentials,
		routeOperatorDialOptions: opts.RouteOperatorDialOptions,
		birdDialer:               opts.BIRDDialer,
		log:                      opts.Log,
	}
}

// Close stops accepting new imports, cancels every import and setup in
// progress and waits for them to exit until ctx is done.
//
// A close that runs out of time reports it and leaves the service closed;
// a later call waits for the same imports again.
func (m *AdapterService) Close(ctx context.Context) error {
	m.beginClose()

	select {
	case <-m.stopped:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("BIRD imports did not stop in time: %w", context.Cause(ctx))
	}
}

// beginClose refuses new setups and cancels the running ones, once.
func (m *AdapterService) beginClose() {
	m.importsMu.Lock()
	defer m.importsMu.Unlock()

	if m.closed {
		return
	}
	m.closed = true
	m.cancel()
	go func() {
		m.inflight.Wait()
		close(m.stopped)
	}()
}

// ListSessions returns information about all active BIRD import sessions.
func (m *AdapterService) ListSessions(
	ctx context.Context,
	req *adapterpb.ListSessionsRequest,
) (*adapterpb.ListSessionsResponse, error) {
	m.importsMu.Lock()
	defer m.importsMu.Unlock()

	sessions := make([]*adapterpb.SessionInfo, 0, len(m.imports))
	for name, holder := range m.imports {
		sessions = append(sessions, holder.ToSessionInfo(name))
	}

	return &adapterpb.ListSessionsResponse{
		Sessions: sessions,
	}, nil
}

func (m *AdapterService) SetupConfig(
	ctx context.Context,
	req *adapterpb.SetupConfigRequest,
) (*adapterpb.SetupConfigResponse, error) {
	mplsV4Src := req.GetSourceV4().ToAddr()
	mplsV6Src := req.GetSourceV6().ToAddr()

	cfg := bird.DefaultConfig()
	req.GetConfig().ToConfig(cfg)

	params := ImportParams{
		Name:     req.GetName(),
		Config:   cfg,
		SourceV4: mplsV4Src,
		SourceV6: mplsV6Src,
		LogLevel: req.GetConfig().GetLogLevel(),
	}
	if err := validateImportParams(params); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := m.SetupImport(params); err != nil {
		code := codes.Internal
		if status.Code(err) == codes.Unavailable {
			// The route operator is not reachable yet, so the client may retry.
			code = codes.Unavailable
		}
		return nil, status.Error(code, err.Error())
	}

	return &adapterpb.SetupConfigResponse{}, nil
}

// ImportParams describes a single BIRD import to set up.
type ImportParams struct {
	Name     string
	Config   *bird.Config
	SourceV4 netip.Addr
	SourceV6 netip.Addr
	LogLevel string
}

// validateImportParams returns an error when the parameters cannot describe an
// import.
func validateImportParams(params ImportParams) error {
	if !params.SourceV4.Is4() {
		return fmt.Errorf("v4 source %q is not an IPv4 address", params.SourceV4)
	}
	if !params.SourceV6.Is6() || params.SourceV6.Is4In6() {
		return fmt.Errorf("v6 source %q is not a pure IPv6 address", params.SourceV6)
	}

	return nil
}

// SetupImport configures and starts a BIRD import, bypassing SetupConfig's
// protobuf decoding.
//
// The import replaces the one of the same name only once it has its
// connection and its first stream, so a failed setup leaves the running
// import alone. The call returns after the replaced import has exited. It
// takes no context.Context: the import outlives the call that started it,
// and only a replacement or the service close stops it.
func (m *AdapterService) SetupImport(params ImportParams) error {
	if err := validateImportParams(params); err != nil {
		return err
	}

	m.log.Info("setting up the configuration",
		zap.String("name", params.Name),
		zap.String("log_level", params.LogLevel),
	)

	if err := m.admitSetup(); err != nil {
		return err
	}
	defer m.inflight.Done()

	holder, err := m.prepareImport(params)
	if err != nil {
		return err
	}

	predecessor, err := m.publishImport(holder)
	if err != nil {
		holder.Release()
		return err
	}
	if predecessor != nil {
		<-predecessor.Done()
		m.log.Info("replaced existing BIRD import",
			zap.String("config", params.Name),
		)
	}

	return nil
}

// admitSetup counts a setup in progress unless the service is closed.
func (m *AdapterService) admitSetup() error {
	m.importsMu.Lock()
	defer m.importsMu.Unlock()

	if m.closed {
		return errServiceClosed
	}
	m.inflight.Add(1)
	return nil
}

// routeLog returns the logger for an import's routes, filtered by the
// level the import asks for and silent when it asks for none.
func (m *AdapterService) routeLog(params ImportParams) *zap.Logger {
	if params.LogLevel == "" {
		return zap.NewNop()
	}

	var level zapcore.Level
	if err := level.UnmarshalText([]byte(params.LogLevel)); err != nil {
		m.log.Warn("invalid log level, using nop logger",
			zap.String("name", params.Name),
			zap.String("log_level", params.LogLevel),
			zap.Error(err),
		)
		return zap.NewNop()
	}

	// Wrap the base core with the requested level filter.
	filteredCore := &levelFilterCore{
		Core:  m.log.Core(),
		level: level,
	}
	return zap.New(filteredCore).Named(params.Name)
}

// prepareImport connects to the route operator and opens the first stream
// of an import, sending nothing on it.
func (m *AdapterService) prepareImport(params ImportParams) (*importHolder, error) {
	// gRPC jitters the reconnect delay after capping it, so the cap leaves
	// room for the jitter.
	reconnect := grpcbackoff.DefaultConfig
	reconnect.MaxDelay = time.Duration(float64(importRetryMaxDelay) / (1 + reconnect.Jitter))
	dialOptions := []grpc.DialOption{
		grpc.WithDefaultCallOptions(grpc.UseCompressor(xgrpc.ZstdName)),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           reconnect,
			MinConnectTimeout: routeOperatorMinConnectTimeout,
		}),
		// Pings and the TCP user timeout find a dead connection to the
		// gateway; the heartbeats of a quiet import feed the latter.
		//
		// They cover only the hop to the gateway and only while a stream is
		// open, so they shorten the detection of some failures but guarantee
		// no recovery: the import restarts its attempts on its own.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                routeOperatorKeepaliveTime,
			Timeout:             routeOperatorKeepaliveTimeout,
			PermitWithoutStream: false,
		}),
	}
	dialOptions = append(dialOptions, m.routeOperatorDialOptions...)
	dialOptions = append(dialOptions, grpc.WithTransportCredentials(m.routeOperatorCredentials))
	conn, err := grpc.NewClient(m.routeOperatorEndpoint, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the route operator endpoint: %w", err)
	}

	holder := newImportHolder(
		m.ctx,
		conn,
		params,
		withImportHolderRouteLog(m.routeLog(params)),
		withImportHolderBIRDDialer(m.birdDialer),
		withImportHolderLog(m.log.With(zap.String("config", params.Name))),
	)
	if err := holder.Open(); err != nil {
		holder.Release()
		return nil, fmt.Errorf("failed to setup initial BIRD import stream: %w", err)
	}

	return holder, nil
}

// publishImport makes holder the import of its name and starts it, and
// returns the import it replaces, already cancelled.
//
// The new import sends nothing before the replaced one has exited, and an
// import exits only after its own predecessor, so one adapter never feeds
// the same RIB from two streams at once, however many setups race.
func (m *AdapterService) publishImport(holder *importHolder) (*importHolder, error) {
	m.importsMu.Lock()
	defer m.importsMu.Unlock()

	if m.closed {
		return nil, errServiceClosed
	}

	predecessor := m.imports[holder.Name]
	if predecessor != nil {
		predecessor.Cancel()
	}
	m.imports[holder.Name] = holder

	m.inflight.Go(func() {
		holder.Run(predecessor)
	})

	return predecessor, nil
}

// importHolder owns one BIRD import: its connection to the route operator,
// its MPLS sync and the attempts that feed the RIB one after another.
type importHolder struct {
	// Name is the RIB the import feeds.
	Name      string
	config    *bird.Config
	createdAt time.Time // Timestamp when the session was created
	// ctx lives as long as the import; cancel stops the import, its
	// attempts and the wait between them.
	ctx    context.Context
	cancel context.CancelFunc
	conn   *grpc.ClientConn // gRPC connection to the route operator's RouteService
	// mplsSync is shared by the attempts, which never overlap.
	mplsSync *mpls.Sync
	// first is the attempt opened by the setup, nil until then and without
	// sockets.
	first *importAttempt
	// done is closed once the import has exited and released its
	// connection.
	done chan struct{}
	// birdDialer connects to the BIRD export sockets when set.
	birdDialer bird.Dialer
	// routeLog reports single routes at the level the import asks for, and
	// log reports the import itself.
	routeLog, log *zap.Logger
}

type importHolderOption func(*importHolderOptions)

type importHolderOptions struct {
	BIRDDialer    bird.Dialer
	RouteLog, Log *zap.Logger
}

func newImportHolderOptions() *importHolderOptions {
	return &importHolderOptions{
		RouteLog: zap.NewNop(),
		Log:      zap.NewNop(),
	}
}

// withImportHolderRouteLog sets the logger for single routes.
func withImportHolderRouteLog(log *zap.Logger) importHolderOption {
	return func(o *importHolderOptions) {
		o.RouteLog = log
	}
}

// withImportHolderBIRDDialer sets the function that connects to the BIRD
// export sockets, the reader's own dialer when nil.
func withImportHolderBIRDDialer(dial bird.Dialer) importHolderOption {
	return func(o *importHolderOptions) {
		o.BIRDDialer = dial
	}
}

// withImportHolderLog sets the logger for the import itself.
func withImportHolderLog(log *zap.Logger) importHolderOption {
	return func(o *importHolderOptions) {
		o.Log = log
	}
}

// newImportHolder builds an import that lives until ctx is done or it is
// cancelled, and that owns conn from now on.
func newImportHolder(
	ctx context.Context,
	conn *grpc.ClientConn,
	params ImportParams,
	options ...importHolderOption,
) *importHolder {
	opts := newImportHolderOptions()
	for _, o := range options {
		o(opts)
	}

	ctx, cancel := context.WithCancel(ctx)
	return &importHolder{
		Name:      params.Name,
		config:    params.Config,
		createdAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
		conn:      conn,
		// The sync keeps the route-mpls config in step with the MPLS
		// routes, across control-plane restarts and failed pushes.
		//
		// It logs to the import logger, so a lost route-mpls config or a
		// route dropped for its label is reported whatever log level the
		// import sets.
		mplsSync: mpls.NewSync(
			params.Name,
			routemplspb.NewRouteMPLSServiceClient(conn),
			params.SourceV4,
			params.SourceV6,
			mpls.WithLog(opts.Log),
		),
		done:       make(chan struct{}),
		birdDialer: opts.BIRDDialer,
		routeLog:   opts.RouteLog,
		log:        opts.Log,
	}
}

// Open opens the stream of the first attempt, sending nothing on it, so
// that a route operator out of reach fails the setup.
//
// An import without sockets reads nothing and opens no stream.
func (m *importHolder) Open() error {
	if len(m.config.Sockets) == 0 {
		return nil
	}

	first, err := m.openAttempt()
	if err != nil {
		return err
	}
	m.first = first
	return nil
}

// Cancel stops the import without waiting for it.
func (m *importHolder) Cancel() {
	m.cancel()
}

// Done is closed once the import has exited and released its connection.
func (m *importHolder) Done() <-chan struct{} {
	return m.done
}

// Release cancels the import and closes its connection.
//
// It runs exactly once per import: when the import exits, or when its setup
// fails before the import started.
func (m *importHolder) Release() {
	m.cancel()
	_ = m.conn.Close()
}

// ToSessionInfo builds the public session summary for this import, keyed
// by name.
func (m *importHolder) ToSessionInfo(name string) *adapterpb.SessionInfo {
	connState := adapterpb.ConnectionState_CONNECTION_STATE_UNKNOWN
	switch m.conn.GetState() {
	case connectivity.Idle:
		connState = adapterpb.ConnectionState_CONNECTION_STATE_IDLE
	case connectivity.Connecting:
		connState = adapterpb.ConnectionState_CONNECTION_STATE_CONNECTING
	case connectivity.Ready:
		connState = adapterpb.ConnectionState_CONNECTION_STATE_READY
	case connectivity.TransientFailure:
		connState = adapterpb.ConnectionState_CONNECTION_STATE_TRANSIENT_FAILURE
	case connectivity.Shutdown:
		connState = adapterpb.ConnectionState_CONNECTION_STATE_SHUTDOWN
	}

	return &adapterpb.SessionInfo{
		Name:            name,
		Sockets:         m.config.Sockets,
		CreatedAt:       m.createdAt.UnixNano(),
		ConnectionState: connState,
	}
}

// openAttempt opens the stream of a new attempt.
func (m *importHolder) openAttempt() (*importAttempt, error) {
	ctx, cancel := context.WithCancelCause(m.ctx)
	// The stream is never half-closed: cancelling the attempt's context is
	// the only way the adapter ends it.
	stream, err := routepb.NewRouteServiceClient(m.conn).FeedRIB(ctx)
	if err != nil {
		cancel(err)
		return nil, err
	}

	return &importAttempt{
		ctx:    ctx,
		cancel: cancel,
		stream: stream,
	}, nil
}

// Run feeds the RIB until the import is cancelled, starting a fresh
// attempt after each one ends, and releases the import on return.
//
// Every attempt replays the whole BIRD table, since BIRD exports it in full
// to each new connection. The import waits for predecessor to exit before
// its first attempt, and before it exits itself even when cancelled first,
// so that its own successor never overlaps the predecessor. The wait is
// short: a replaced import has always been cancelled.
func (m *importHolder) Run(predecessor *importHolder) {
	defer close(m.done)
	defer m.Release()

	if predecessor != nil {
		<-predecessor.Done()
		if m.ctx.Err() != nil {
			return
		}
	}

	if len(m.config.Sockets) == 0 {
		m.log.Info("bird import is disabled, no sockets configured")
		return
	}

	retry := backoff.NewExponentialBackOff()
	retry.MaxInterval = importRetryMaxDelay
	retry.Reset()

	attempt := m.first
	m.first = nil
	for {
		if attempt == nil {
			next, err := m.openAttempt()
			if err != nil {
				if m.ctx.Err() != nil {
					return
				}
				m.log.Warn("failed to open the BIRD import stream",
					zap.Error(err),
				)
				if !m.waitRetry(retry) {
					return
				}
				continue
			}
			attempt = next
		}

		startedAt := time.Now()
		err := attempt.Run(m.newExport(attempt))
		attempt = nil
		if m.ctx.Err() != nil {
			m.log.Info("stopped BIRD import")
			return
		}

		// The route operator closes the stream itself only when another
		// writer of the same RIB has taken the session over, so that ending
		// never counts as a healthy run.
		if errors.Is(err, errFeedRIBClosedByServer) {
			m.log.Warn("route operator closed the BIRD import stream, another writer of the same RIB likely superseded it",
				zap.Error(err),
			)
		} else {
			m.log.Warn("bird import attempt ended, restarting",
				zap.Error(err),
			)
			if time.Since(startedAt) >= importHealthyUptime {
				retry.Reset()
			}
		}

		if !m.waitRetry(retry) {
			return
		}
	}
}

// waitRetry waits out the next retry delay and reports whether the import
// is still running.
func (m *importHolder) waitRetry(retry backoff.BackOff) bool {
	timer := time.NewTimer(min(retry.NextBackOff(), importRetryMaxDelay))
	defer timer.Stop()

	select {
	case <-m.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// newExport builds the BIRD reader of one attempt, whose callbacks send on
// that attempt's stream only.
//
// The reader cancels its callbacks' context when a BIRD socket fails, but a
// send blocked by flow control waits on the attempt's context instead. Each
// sending callback therefore passes the cancellation of its own context on
// to the attempt for as long as it runs.
func (m *importHolder) newExport(attempt *importAttempt) *bird.Export {
	onUpdate := func(ctx context.Context, routes []rib.Route) error {
		stop := attempt.CancelWith(ctx)
		defer stop()

		m.log.Debug("processing BIRD routes",
			zap.Int("count", len(routes)),
		)

		for idx := range routes {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}

			// Log if NextHop is invalid before converting to protobuf
			if !routes[idx].NextHop.IsValid() {
				m.routeLog.Debug("route has invalid next_hop, skip",
					zap.String("prefix", routes[idx].Prefix.String()),
					zap.String("next_hop", routes[idx].NextHop.String()),
					zap.Binary("next_hop_bytes", routes[idx].NextHop.AsSlice()),
				)
				continue
			}

			if routes[idx].RD != 0 {
				// Assume it is a MPLS route
				m.mplsSync.Apply(routes[idx])
				continue
			}

			route, err := rib.ToPBRoute(&routes[idx])
			if err != nil {
				m.routeLog.Debug("route cannot be converted to protobuf, skip",
					zap.String("prefix", routes[idx].Prefix.String()),
					zap.Error(err),
				)
				continue
			}

			err = attempt.Send(&routepb.Update{
				Name:     m.Name,
				IsDelete: routes[idx].ToRemove,
				Route:    route,
			})
			if err != nil {
				return fmt.Errorf("send BIRD route update for %s failed: %w", routes[idx].Prefix, err)
			}
		}

		m.pushMPLS(ctx)

		return nil
	}

	// An update without a route asks the route operator to commit the
	// routes sent so far.
	onFlush := func(ctx context.Context) error {
		stop := attempt.CancelWith(ctx)
		defer stop()

		if err := attempt.Send(&routepb.Update{Name: m.Name}); err != nil {
			return fmt.Errorf("flush BIRD routes failed: %w", err)
		}
		return nil
	}

	// An idle tick sends a heartbeat once the stream has been silent for the
	// heartbeat interval; its unacknowledged bytes let the kernel find a
	// half-open connection while BIRD is quiet.
	//
	// A failed heartbeat ends the attempt. Idle ticks push MPLS too, so a
	// failed push is retried even while BIRD sends nothing.
	onIdle := func(ctx context.Context) {
		stop := attempt.CancelWith(ctx)
		defer stop()

		if attempt.SinceLastSend() >= importHeartbeatInterval {
			if err := attempt.Send(&routepb.Update{Name: m.Name, Heartbeat: true}); err != nil {
				attempt.Cancel(fmt.Errorf("BIRD import heartbeat failed: %w", err))
				return
			}
		}
		m.pushMPLS(ctx)
	}

	options := []bird.ExportReaderOption{
		bird.WithExportReaderIdle(onIdle),
		bird.WithExportReaderLog(m.routeLog),
	}
	if m.birdDialer != nil {
		options = append(options, bird.WithExportReaderDialer(m.birdDialer))
	}
	return bird.NewExportReader(m.config, onUpdate, onFlush, options...)
}

// pushMPLS delivers the MPLS changes, only logging a failure so that it
// does not hold back the IP routes: the sync sends the whole table once
// route-mpls recovers.
func (m *importHolder) pushMPLS(ctx context.Context) {
	if err := m.mplsSync.Push(ctx); err != nil {
		m.log.Warn("failed to push mpls routes",
			zap.Error(err),
		)
	}
}

// importAttempt is one restart unit of an import: a stream opened with the
// attempt's own context, its receiver and one run of the BIRD reader.
type importAttempt struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	stream grpc.ClientStreamingClient[routepb.Update, routepb.UpdateSummary]
	// lastSend is when the reader's goroutine, the only sender, last sent
	// an update.
	lastSend time.Time
}

// Run runs export and the receiver of the stream until either ends, and
// returns why the attempt ended.
//
// Whichever ends first cancels the attempt, which stops the other one, and
// the call returns only after both have exited.
func (m *importAttempt) Run(export *bird.Export) error {
	defer m.cancel(nil)

	var workers errgroup.Group
	workers.Go(func() error {
		// The receive returns once the call ends, without an error when the
		// route operator closed it with a summary.
		//
		// It never half-closes the stream, so it may run beside the sender.
		err := m.stream.RecvMsg(&routepb.UpdateSummary{})
		if err == nil {
			err = errFeedRIBClosedByServer
		}
		m.cancel(fmt.Errorf("BIRD import stream ended: %w", err))
		return nil
	})
	workers.Go(func() error {
		err := export.Run(m.ctx)
		m.cancel(fmt.Errorf("BIRD export reader stopped: %w", err))
		return nil
	})
	_ = workers.Wait()

	return context.Cause(m.ctx)
}

// Send sends one update on the attempt's stream.
func (m *importAttempt) Send(update *routepb.Update) error {
	if err := m.stream.Send(update); err != nil {
		return err
	}
	m.lastSend = time.Now()
	return nil
}

// SinceLastSend reports how long the attempt has sent nothing on its
// stream; only the sending goroutine may call it.
func (m *importAttempt) SinceLastSend() time.Duration {
	return time.Since(m.lastSend)
}

// Cancel ends the attempt with cause.
func (m *importAttempt) Cancel(cause error) {
	m.cancel(cause)
}

// CancelWith cancels the attempt with the cause of ctx once ctx is done,
// until the returned function is called.
func (m *importAttempt) CancelWith(ctx context.Context) func() bool {
	return context.AfterFunc(ctx, func() {
		m.cancel(context.Cause(ctx))
	})
}
