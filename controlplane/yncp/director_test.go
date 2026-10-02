package yncp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	"github.com/yanet-platform/yanet2/controlplane/yncp"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	dscp "github.com/yanet-platform/yanet2/modules/dscp/controlplane"
)

// newDirectorConfig creates a ready fixture with an ephemeral local endpoint.
func newDirectorConfig(t *testing.T) *yncp.Config {
	t.Helper()
	config := yncp.DefaultConfig()
	config.MemoryPath = testshm.NewStorage(t, testshm.StorageTypes{})
	config.Gateway.InstanceID = xcfg.NewRequired(uint32(0))
	config.Gateway.Server.Endpoint = "127.0.0.1:0"
	config.Gateway.Server.HTTPEndpoint = ""
	module := decap.DefaultConfig()
	module.InstanceID = xcfg.NewRequired(uint32(0))
	module.MemoryPath = xcfg.MustNonEmptyString(config.MemoryPath)
	config.Modules.Decap = xcfg.NewOptional(*module)
	return config
}

// Test_Director_BundleConstructorErrorReleasesSharedMemory verifies that a
// module creation failure is reported and its mapping is released.
func Test_Director_BundleConstructorErrorReleasesSharedMemory(t *testing.T) {
	config := newDirectorConfig(t)
	config.Modules.Decap.Unwrap().MemoryPath = xcfg.MustNonEmptyString(filepath.Join(t.TempDir(), "missing"))
	director, err := yncp.NewDirector(config)
	if director != nil {
		t.Cleanup(func() { require.NoError(t, director.Close()) })
	}
	require.Nil(t, director)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "failed to initialize bundle")
	require.ErrorContains(t, err, "decap module")
}

// Test_Director_GatewayConstructorErrorReleasesServices verifies that a
// proxy setup failure closes earlier services and releases shared memory.
func Test_Director_GatewayConstructorErrorReleasesServices(t *testing.T) {
	config := newDirectorConfig(t)
	dscpConfig := dscp.DefaultConfig()
	dscpConfig.InstanceID = xcfg.NewRequired(uint32(0))
	dscpConfig.MemoryPath = xcfg.MustNonEmptyString(config.MemoryPath)
	config.Modules.DSCP = xcfg.NewOptional(*dscpConfig)
	missing := xcfg.MustNonEmptyString(filepath.Join(t.TempDir(), "missing"))
	config.Gateway.Server.TLS = &gateway.TLSConfig{CertFile: missing, KeyFile: missing}
	director, err := yncp.NewDirector(config)
	if director != nil {
		t.Cleanup(func() { require.NoError(t, director.Close()) })
	}
	require.Nil(t, director)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "failed to create gateway")
}
