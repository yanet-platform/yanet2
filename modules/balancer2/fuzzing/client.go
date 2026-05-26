// This file defines the balancer2 RPC client abstraction used by the
// fuzzing runner. The exported BalancerRPC interface keeps the runner
// decoupled from the generated gRPC client so that tests can substitute a
// recording fake; RPCClient is the production implementation that wraps
// balancerpb.BalancerClient and records a latency sample per call. The
// The package no longer performs startup config/session mutation RPCs:
// fuzzing assumes the target config already exists.

package fuzzing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// RPC names used as LatencyStats keys. They are kept distinct from the
// OperationType iota constants in operations.go (OpUpdateVS, etc.) so
// the two namespaces — gRPC method names vs operation kinds — do not
// collide. The string values match the protobuf service method names
// one-to-one.
const (
	RPCUpdateConfig = "UpdateConfig"
	RPCUpdateVS    = "UpdateVS"
	RPCDeleteVS    = "DeleteVS"
	RPCUpdateReals = "UpdateReals"
	RPCGetState    = "GetState"
)

// BalancerRPC is the narrow surface the fuzzing runner needs from the
// balancer2 controlplane. Method signatures intentionally take pointer
// request types so they round-trip identically to the generated client;
// every implementation must record exactly one latency sample per call
// under the matching RPC* constant.
type BalancerRPC interface {
	UpdateConfig(
		ctx context.Context,
		req *balancerpb.UpdateConfigRequest,
	) (*balancerpb.UpdateConfigResponse, error)
	UpdateVS(
		ctx context.Context,
		req *balancerpb.UpdateVSRequest,
	) (*balancerpb.UpdateVSResponse, error)
	DeleteVS(
		ctx context.Context,
		req *balancerpb.DeleteVSRequest,
	) (*balancerpb.DeleteVSResponse, error)
	UpdateReals(
		ctx context.Context,
		req *balancerpb.UpdateRealsRequest,
	) (*balancerpb.UpdateRealsResponse, error)
	GetState(
		ctx context.Context,
		req *balancerpb.GetStateRequest,
	) (*balancerpb.GetStateResponse, error)
}

// RPCClient is the production BalancerRPC backed by the generated
// balancerpb.BalancerClient. Per-call deadlines are derived from
// requestTimeout, and every call records one sample into stats keyed by
// the matching RPC* constant.
//
// RPCClient is safe for sequential use from the single-threaded fuzzing
// runner; the underlying grpc.ClientConn is itself concurrency-safe,
// but stats is not — see LatencyStats docs.
type RPCClient struct {
	client         balancerpb.BalancerClient
	stats          *LatencyStats
	requestTimeout time.Duration
	now            func() time.Time
}

// NewRPCClient wraps an existing BalancerClient. It is the primary
// constructor used by tests (which substitute a fake client) and by
// DialRPCClient. requestTimeout must be positive; stats must be non-nil.
func NewRPCClient(
	client balancerpb.BalancerClient,
	requestTimeout time.Duration,
	stats *LatencyStats,
) (*RPCClient, error) {
	if client == nil {
		return nil, errors.New("rpc client: balancer client must not be nil")
	}
	if stats == nil {
		return nil, errors.New("rpc client: stats must not be nil")
	}
	if requestTimeout <= 0 {
		return nil, fmt.Errorf("rpc client: request_timeout must be a positive duration, got %v",
			requestTimeout)
	}
	return &RPCClient{
		client:         client,
		stats:          stats,
		requestTimeout: requestTimeout,
		now:            time.Now,
	}, nil
}

// DialRPCClient establishes an insecure gRPC connection to
// cfg.Endpoint and returns an RPCClient plus a closer that releases the
// underlying connection. The caller owns the closer and must invoke it
// during shutdown.
//
// Insecure credentials match the rest of the in-repo Yanet operators
// (see e.g. operators/bird-adapter/cmd/yanet-bird-adapter/client.go);
// the fuzzer is intended to run alongside the controlplane on the same
// host.
func DialRPCClient(
	cfg *RuntimeConfig,
	stats *LatencyStats,
) (*RPCClient, func() error, error) {
	if cfg == nil {
		return nil, nil, errors.New("rpc client: runtime config must not be nil")
	}
	conn, err := grpc.NewClient(
		cfg.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("rpc client: dial %q: %w", cfg.Endpoint, err)
	}
	client, err := NewRPCClient(balancerpb.NewBalancerClient(conn), cfg.RequestTimeout, stats)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return client, conn.Close, nil
}

// callContext returns a derived context bounded by the configured
// per-RPC timeout. The returned cancel must be invoked by the caller.
func (m *RPCClient) callContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, m.requestTimeout)
}

// record stores one latency sample for op measured from start to
// m.now(). It is called from a deferred wrapper on every RPC method.
func (m *RPCClient) record(op string, start time.Time, err error) {
	m.stats.Record(op, m.now().Sub(start), err)
}

// UpdateConfig invokes Balancer.UpdateConfig with a derived deadline and
// records the call latency under RPCUpdateConfig.
func (m *RPCClient) UpdateConfig(
	ctx context.Context,
	req *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
	callCtx, cancel := m.callContext(ctx)
	defer cancel()
	start := m.now()
	resp, err := m.client.UpdateConfig(callCtx, req)
	m.record(RPCUpdateConfig, start, err)
	return resp, err
}

// UpdateVS invokes Balancer.UpdateVS with a derived deadline and
// records the call latency under RPCUpdateVS.
func (m *RPCClient) UpdateVS(
	ctx context.Context,
	req *balancerpb.UpdateVSRequest,
) (*balancerpb.UpdateVSResponse, error) {
	callCtx, cancel := m.callContext(ctx)
	defer cancel()
	start := m.now()
	resp, err := m.client.UpdateVS(callCtx, req)
	m.record(RPCUpdateVS, start, err)
	return resp, err
}

// DeleteVS invokes Balancer.DeleteVS with a derived deadline and
// records the call latency under RPCDeleteVS.
func (m *RPCClient) DeleteVS(
	ctx context.Context,
	req *balancerpb.DeleteVSRequest,
) (*balancerpb.DeleteVSResponse, error) {
	callCtx, cancel := m.callContext(ctx)
	defer cancel()
	start := m.now()
	resp, err := m.client.DeleteVS(callCtx, req)
	m.record(RPCDeleteVS, start, err)
	return resp, err
}

// UpdateReals invokes Balancer.UpdateReals with a derived deadline and
// records the call latency under RPCUpdateReals.
func (m *RPCClient) UpdateReals(
	ctx context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	callCtx, cancel := m.callContext(ctx)
	defer cancel()
	start := m.now()
	resp, err := m.client.UpdateReals(callCtx, req)
	m.record(RPCUpdateReals, start, err)
	return resp, err
}

// GetState invokes Balancer.GetState with a derived deadline and
// records the call latency under RPCGetState.
func (m *RPCClient) GetState(
	ctx context.Context,
	req *balancerpb.GetStateRequest,
) (*balancerpb.GetStateResponse, error) {
	callCtx, cancel := m.callContext(ctx)
	defer cancel()
	start := m.now()
	resp, err := m.client.GetState(callCtx, req)
	m.record(RPCGetState, start, err)
	return resp, err
}
