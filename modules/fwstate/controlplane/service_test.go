package fwstate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// TestValidateSyncPorts verifies that ports above the uint16 range are
// rejected with InvalidArgument, while zero and boundary values pass.
func TestValidateSyncPorts(t *testing.T) {
	cases := []struct {
		name          string
		portMulticast uint32
		portUnicast   uint32
		wantErr       bool
	}{
		{
			name:          "both zero",
			portMulticast: 0,
			portUnicast:   0,
			wantErr:       false,
		},
		{
			name:          "boundary value",
			portMulticast: 65535,
			portUnicast:   65535,
			wantErr:       false,
		},
		{
			name:          "multicast port just above boundary",
			portMulticast: 65536,
			portUnicast:   0,
			wantErr:       true,
		},
		{
			name:          "unicast port far above boundary",
			portMulticast: 0,
			portUnicast:   70000,
			wantErr:       true,
		},
		{
			name:          "one valid one out of range",
			portMulticast: 1,
			portUnicast:   70000,
			wantErr:       true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &fwstatepb.SyncConfig{
				PortMulticast: tc.portMulticast,
				PortUnicast:   tc.portUnicast,
			}

			err := validateSyncPorts(cfg)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
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
