package bird_adapter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"

	"github.com/yanet-platform/yanet2/common/commonpb"
	adapterpb "github.com/yanet-platform/yanet2/modules/route/bird-adapter/proto"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/bird"
	"github.com/yanet-platform/yanet2/modules/route/internal/rib"
)

// AdapterService implements the Adapter gRPC service for the route module.
type AdapterService struct {
	adapterpb.UnimplementedAdapterServiceServer

	importsMu       sync.Mutex
	imports         map[string]*importHolder
	gatewayEndpoint string    // gRPC endpoint of the RouteService (gateway) for RIB updates
	quitCh          chan bool // Signals all background BIRD import loops to stop
	log             *zap.SugaredLogger
}

func NewAdapterService(
	gatewayEndpoint string,
	log *zap.SugaredLogger,
) *AdapterService {
	return &AdapterService{
		imports:         make(map[string]*importHolder),
		gatewayEndpoint: gatewayEndpoint,
		quitCh:          make(chan bool),
		log:             log,
	}
}

func (m *AdapterService) SetupConfig(
	ctx context.Context,
	req *adapterpb.SetupConfigRequest,
) (*adapterpb.SetupConfigResponse, error) {
	target := req.GetTarget()

	m.log.Infow("setting up the configuration",
		zap.String("name", target.ConfigName),
	)

	cfg := bird.DefaultConfig()
	req.GetConfig().ToConfig(cfg)
	if len(cfg.Sockets) == 0 {
		// We do not need this connection if there is no background stream for import
		return nil, fmt.Errorf("no export sockets provided")
	}

	conn, err := grpc.NewClient(
		m.gatewayEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the gateway: %w", err)
	}

	// And then add dynamic routes, if any.
	if err := m.processBirdImport(conn, cfg, target); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to setup bird import reader: %w ", err)
	}

	return &adapterpb.SetupConfigResponse{}, nil
}

var errStreamClosed = fmt.Errorf("stream closed")

// importHolder bundles resources for one BIRD import: the BIRD data reader,
// a cancellable context for its goroutines, the gRPC connection to the RIB service,
// and the active gRPC stream for sending updates.
type importHolder struct {
	export        *bird.Export                                                       // Reads/parses routes from BIRD
	cancel        context.CancelFunc                                                 // Stops this import's goroutines (runBirdImportLoop, export.Run)
	conn          *grpc.ClientConn                                                   // gRPC connection to RouteService (gateway)
	currentStream *grpc.ClientStreamingClient[routepb.Update, routepb.UpdateSummary] // Active gRPC stream for RIB updates; replaced on reconnect
}

// processBirdImport streams BIRD route updates to the control plane RIB.
// Handles automatic reconnection and graceful cleanup of existing imports.
// It establishes the initial gRPC stream to the RouteService (gateway), sets up
// callbacks for the bird.Export reader, and manages replacement of existing imports.
func (m *AdapterService) processBirdImport(conn *grpc.ClientConn, cfg *bird.Config, target *commonpb.TargetModule) error {
	// streamCtx governs this specific import's gRPC stream and BIRD reader.
	// Cancelled via holder.cancel on replacement or service stop.
	streamCtx, cancel := context.WithCancel(context.Background())
	client := routepb.NewRouteServiceClient(conn)
	stream, err := client.FeedRIB(streamCtx)
	if err != nil {
		cancel() // cleanup context if stream setup fails
		return fmt.Errorf("failed to setup initial BIRD import stream: %w", err)
	}

	holder := new(importHolder)
	holder.currentStream = &stream
	log := m.log.With("config", target.ConfigName)

	// onUpdate sends route batches over the gRPC stream. Called by bird.Export.
	onUpdate := func(ctx context.Context, routes []rib.Route) error {
		log.Debugf("processing %d BIRD routes", len(routes))
		for idx := range routes {
			select {
			case <-ctx.Done():
				log.Warnf("update stream send cancelled: %v", ctx.Err())
				_, closeErr := (*holder.currentStream).CloseAndRecv()
				return errors.Join(ctx.Err(), closeErr, errStreamClosed) // Signal runBirdImportLoop
			default:
			}

			err := (*holder.currentStream).Send(&routepb.Update{
				Target:   target,
				IsDelete: routes[idx].ToRemove,
				Route:    routepb.FromRIBRoute(&routes[idx], false /* isBest unknown */),
			})
			if err != nil {
				// This error stops bird.Export, triggering reconnection in runBirdImportLoop
				return fmt.Errorf("send BIRD route update for %s failed: %w", routes[idx].Prefix, err)
			}
		}
		return nil
	}

	// onFlush commits updates to dataplane. Called by bird.Export.
	onFlush := func() error {
		// update without route indicates flush event
		err := (*holder.currentStream).Send(&routepb.Update{Target: target})
		if err != nil {
			return fmt.Errorf("flush BIRD routes failed: %w", err)
		}
		return nil
	}

	export := bird.NewExportReader(cfg, onUpdate, onFlush, log)

	// Lock to safely access and modify m.imports.
	m.importsMu.Lock()
	defer m.importsMu.Unlock()
	// Ensure only one active import per target: stop and replace if one exists.
	if oldHolder, ok := m.imports[target.ConfigName]; ok {
		log.Info("replacing existing BIRD import")
		if oldHolder.cancel != nil { // Defensive check
			oldHolder.cancel()
		}
		if oldHolder.conn != nil { // Defensive check
			_ = oldHolder.conn.Close()
		}
	}

	holder.export = export
	holder.cancel = cancel
	holder.conn = conn
	m.imports[target.ConfigName] = holder

	// Launch goroutine for BIRD reading and stream lifecycle management.
	go m.runBirdImportLoop(streamCtx, holder, client, log)

	return nil
}

// runBirdImportLoop is the main goroutine for an active BIRD import.
// It runs the BIRD data reader (holder.export.Run) and, if the reader or gRPC stream fails,
// attempts to re-establish the stream via reconnectStream.
// Terminates if its context (ctx) is cancelled or the service's quitCh is closed.
func (m *AdapterService) runBirdImportLoop(
	ctx context.Context,
	holder *importHolder,
	client routepb.RouteServiceClient,
	log *zap.SugaredLogger,
) {
	defer func() { // Cleanup on exit
		log.Info("BIRD import loop cleanup: closing connection and cancelling context")
		holder.cancel()         // Ensure BIRD reader's context is cancelled
		_ = holder.conn.Close() // Close gRPC client connection
	}()

	runBackoff := backoff.ExponentialBackOff{
		InitialInterval:     backoff.DefaultInitialInterval,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         time.Minute,
	}
	runBackoff.Reset()
	backoffResetTimeout := 10 * time.Minute

	streamActive := true

	for {
		select {
		case <-ctx.Done():
			log.Infow("BIRD import loop cancelled via context", zap.Error(ctx.Err()))
			return
		case <-m.quitCh:
			log.Info("BIRD import loop stopping due to service quit signal")
			return
		default:
		}

		if holder.conn.GetState() == connectivity.Shutdown {
			log.Error("gRPC connection for BIRD import is shutdown, terminating loop")
			return
		}

		if !streamActive {
			log.Info("attempting to re-establish BIRD route update stream")
			if !m.reconnectStream(ctx, client, holder.currentStream, log) {
				log.Info("stream reconnection aborted, terminating BIRD import loop")
				return // Reconnect failed due to ctx / quitCh
			}
			streamActive = true
			log.Info("successfully re-established BIRD route update stream")
		}

		log.Info("starting BIRD export reader")
		lastRunAttempt := time.Now()
		err := holder.export.Run(ctx) // Blocking call
		if err != nil {
			log.Warnw("BIRD export reader stopped with error", zap.Error(err))
			streamActive = false // Stream needs re-establishment

			// If context cancellation caused reader to stop, exit loop
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Warn("BIRD export reader context cancelled, terminating loop")
				return
			}

			// If stream wasn't closed by onUpdate's error path, try to close it here
			if !errors.Is(err, errStreamClosed) {
				log.Info("closing client stream after BIRD export reader error")
				if _, closeErr := (*holder.currentStream).CloseAndRecv(); closeErr != nil {
					log.Warnw("error closing client stream post-reader failure", zap.Error(closeErr))
				}
			}

			if time.Since(lastRunAttempt) > backoffResetTimeout {
				runBackoff.Reset()
			}
			// Apply exponential backoff before retrying the export reader
			select {
			case <-ctx.Done():
				log.Infow("BIRD import loop cancelled via context", zap.Error(ctx.Err()))
				return
			case <-m.quitCh:
				log.Info("BIRD import loop stopping due to service quit signal")
				return
			case <-time.After(runBackoff.NextBackOff()):
			}
			// Loop continues to attempt reconnection unless ctx/quitCh terminates it
		} else {
			log.Info("BIRD export reader stopped cleanly, terminating loop")
			return
		}
	}
}

// reconnectStream attempts to re-establish the gRPC stream with exponential backoff.
// Returns true if reconnection succeeds, false if aborted by context or quit signal.
// Updates `currentStream` with the new stream on success.
func (m *AdapterService) reconnectStream(
	ctx context.Context,
	client routepb.RouteServiceClient,
	currentStream *grpc.ClientStreamingClient[routepb.Update, routepb.UpdateSummary],
	log *zap.SugaredLogger,
) bool {
	log.Info("attempting to re-establish BIRD route update stream with exponential backoff")

	ticker := backoff.NewTicker(&backoff.ExponentialBackOff{
		InitialInterval:     backoff.DefaultInitialInterval,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         30 * time.Second,
	})
	defer ticker.Stop()

	for {
		select {
		case <-m.quitCh:
			log.Warn("stream reconnection aborted due to service quit signal")
			return false
		case <-ctx.Done():
			log.Warnw("stream reconnection aborted due to import context cancellation", zap.Error(ctx.Err()))
			return false
		case <-ticker.C:
			log.Info("attempting FeedRIB call for new stream")
			newStream, err := client.FeedRIB(ctx) // Use import's context
			if err != nil {
				log.Warnw("failed to re-establish stream, retrying via ticker", zap.Error(err))
				continue // Ticker schedules next attempt
			}

			*currentStream = newStream // Update to new stream
			return true
		}
	}
}
