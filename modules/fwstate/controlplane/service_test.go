package fwstate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb"
)

// TestValidateSyncConfigDstEther checks that dst_ether validation
// distinguishes between absent and explicitly-zero MAC values.
func TestValidateSyncConfigDstEther(t *testing.T) {
	newConfig := func() *fwstatepb.SyncConfig {
		return &fwstatepb.SyncConfig{
			SrcAddr:          &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
			DstAddrMulticast: &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
			PortMulticast:    1,
		}
	}

	t.Run("missing", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstEther = nil

		err := validateSyncConfig(cfg)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "dst_ether"))
	})

	t.Run("zero", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstEther = &commonpb.MACAddress{}

		err := validateSyncConfig(cfg)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "dst_ether"))
	})

	t.Run("valid", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstEther = commonpb.NewMACAddressEUI48([6]byte{1, 2, 3, 4, 5, 6})

		err := validateSyncConfig(cfg)
		require.NoError(t, err)
	})
}
