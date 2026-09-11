package operator

import (
	"errors"
	"fmt"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/native"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

func newConfigSource(cfg *Config) (desired.Source, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if err := cfg.ValidateSource(); err != nil {
		return nil, err
	}
	switch cfg.Source {
	case "netplan":
		path := DefaultNetplanPath
		if cfg.NetplanPath != nil {
			path = *cfg.NetplanPath
		}
		return &netplan.Source{Path: path}, nil
	case "native":
		return &native.Source{Config: *cfg.Native}, nil
	default:
		return nil, fmt.Errorf("unsupported source %q", cfg.Source)
	}
}
