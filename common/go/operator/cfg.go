package operator

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
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
	// Endpoint is the address the gRPC server binds.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	// AdvertiseEndpoint is the address registered with gateways.
	//
	// Empty uses the bound address. Set it for an unreachable wildcard or
	// loopback bind.
	AdvertiseEndpoint string `yaml:"advertise_endpoint"`
}

// Validate checks the advertised endpoint without resolving its host.
func (m *GRPCServerConfig) Validate() error {
	if m.AdvertiseEndpoint == "" {
		return nil
	}

	host, port, err := net.SplitHostPort(m.AdvertiseEndpoint)
	if err != nil {
		return fmt.Errorf("invalid advertise_endpoint: %w", err)
	}
	if host == "" {
		return fmt.Errorf("invalid advertise_endpoint: host is empty")
	}
	if !isValidEndpointHost(host) {
		return fmt.Errorf("invalid advertise_endpoint host %q", host)
	}

	portNumber, err := net.LookupPort("tcp", port)
	if err != nil || portNumber == 0 {
		return fmt.Errorf("invalid advertise_endpoint port %q", port)
	}

	return nil
}

func isValidEndpointHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}

	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return false
	}

	hasNonNumeric := false
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		for idx := range len(label) {
			character := label[idx]
			switch {
			case 'a' <= character && character <= 'z',
				'A' <= character && character <= 'Z',
				character == '_', character == '-':
				hasNonNumeric = true
			case '0' <= character && character <= '9':
			default:
				return false
			}
		}
	}

	return hasNonNumeric
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
	// InitialBackoff is the first sleep after a failed pass. It grows
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
