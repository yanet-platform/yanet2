package yncp

import (
	"github.com/yanet-platform/yanet2/controlplane/internal/version"
)

// Version returns the current YANET version.
func Version() string {
	return version.Version()
}
