package operator

import (
	"fmt"
	"net"
	"strconv"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// GatewayEndpointOverride selects a named transport and its deployment address.
type GatewayEndpointOverride struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
}

// ApplyGatewayOverrides selects the complete active transport list by name.
//
// The result follows the override order and preserves transport security from
// the host configuration. Invalid overrides leave the input unchanged.
func ApplyGatewayOverrides(
	gateways []GatewayConfig,
	overrides []GatewayEndpointOverride,
) ([]GatewayConfig, error) {
	if len(overrides) == 0 {
		return nil, fmt.Errorf("at least one gateway override is required")
	}

	byName := map[string]GatewayConfig{}
	for _, gateway := range gateways {
		if gateway.Name == "" {
			return nil, fmt.Errorf("configured gateway name is empty")
		}
		if _, exists := byName[gateway.Name]; exists {
			return nil, fmt.Errorf("duplicate configured gateway name %q", gateway.Name)
		}
		byName[gateway.Name] = gateway
	}

	selected := make([]GatewayConfig, 0, len(overrides))
	seen := map[string]bool{}
	for _, override := range overrides {
		if override.Name == "" {
			return nil, fmt.Errorf("gateway override name is empty")
		}
		if seen[override.Name] {
			return nil, fmt.Errorf("duplicate gateway override name %q", override.Name)
		}
		seen[override.Name] = true
		gateway, exists := byName[override.Name]
		if !exists {
			return nil, fmt.Errorf("gateway override %q is not configured", override.Name)
		}
		host, port, err := net.SplitHostPort(override.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("gateway override %q endpoint: %w", override.Name, err)
		}
		portNumber, err := strconv.Atoi(port)
		if host == "" || err != nil || portNumber < 1 || portNumber > 65535 {
			return nil, fmt.Errorf("gateway override %q has invalid endpoint %q", override.Name, override.Endpoint)
		}
		gateway.Endpoint = xcfg.MustNonEmptyString(override.Endpoint)
		selected = append(selected, gateway)
	}

	return selected, nil
}
