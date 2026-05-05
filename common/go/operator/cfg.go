package operator

import (
	"fmt"
	"time"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

const (
	DefaultReconcileInterval       = 30 * time.Second
	DefaultReconcileInitialBackoff = 500 * time.Millisecond
	DefaultReconcileMaxBackoff     = 30 * time.Second
	DefaultRegisterInterval        = 30 * time.Second
)

// GRPCServerConfig describes how to expose the operator's gRPC server.
type GRPCServerConfig struct {
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// GatewayConfig holds the name and gRPC endpoint of a single Gateway.
type GatewayConfig struct {
	// Name is a human-readable label used in logs and status reports.
	Name string `yaml:"name"`
	// Endpoint is the gRPC address of the Gateway.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// RegisterConfig holds the gateway registration heartbeat parameter.
type RegisterConfig struct {
	// Interval sets heartbeat period between registration refreshes.
	Interval xcfg.NonZero[time.Duration] `yaml:"interval"`
}

// ReconcileConfig holds timing parameters for the reconcile loop.
type ReconcileConfig struct {
	// Interval is the steady-state period between successful reconcile
	// passes.
	Interval xcfg.NonZero[time.Duration] `yaml:"interval"`
	// InitialBackoff is the first sleep after a failed pass; grows
	// exponentially.
	InitialBackoff xcfg.NonZero[time.Duration] `yaml:"initial_backoff"`
	// MaxBackoff caps the exponential backoff sleep.
	MaxBackoff xcfg.NonZero[time.Duration] `yaml:"max_backoff"`
}

func (m *ReconcileConfig) Validate() error {
	if m.MaxBackoff.Unwrap() < m.InitialBackoff.Unwrap() {
		return fmt.Errorf(
			"max_backoff (%s) must be >= initial_backoff (%s)",
			m.MaxBackoff.Unwrap(),
			m.InitialBackoff.Unwrap(),
		)
	}

	return nil
}
