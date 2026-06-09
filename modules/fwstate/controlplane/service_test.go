package fwstate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// TestClampBatchSize verifies zero defaulting and upper-bound clamping.
func TestClampBatchSize(t *testing.T) {
	cases := []struct {
		name  string
		input uint32
		want  uint32
	}{
		{
			name:  "zero becomes default",
			input: 0,
			want:  defaultListEntriesBatchSize,
		},
		{
			name:  "below max passes through",
			input: 500,
			want:  500,
		},
		{
			name:  "exactly max passes through",
			input: maxListEntriesBatchSize,
			want:  maxListEntriesBatchSize,
		},
		{
			name:  "above max is clamped to max",
			input: maxListEntriesBatchSize + 1,
			want:  maxListEntriesBatchSize,
		},
		{
			name:  "large value is clamped to max",
			input: 1<<32 - 1,
			want:  maxListEntriesBatchSize,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, clampBatchSize(tc.input))
		})
	}
}

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
