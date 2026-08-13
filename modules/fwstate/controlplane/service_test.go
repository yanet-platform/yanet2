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

// TestValidateSyncPorts verifies that ports above the uint16 range are
// rejected with InvalidArgument, while zero and boundary values pass.
func TestValidateSyncPorts(t *testing.T) {
	cases := []struct {
		name          string
		portMulticast uint32
		wantErr       bool
	}{
		{
			name:          "zero",
			portMulticast: 0,
			wantErr:       false,
		},
		{
			name:          "boundary value",
			portMulticast: 65535,
			wantErr:       false,
		},
		{
			name:          "just above boundary",
			portMulticast: 65536,
			wantErr:       true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &fwstatepb.SyncConfig{
				PortMulticast: tc.portMulticast,
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

// TestValidateSyncConfigMulticastRequired checks that the multicast
// destination pair is validated as required.
func TestValidateSyncConfigMulticastRequired(t *testing.T) {
	newConfig := func() *fwstatepb.SyncConfig {
		return &fwstatepb.SyncConfig{
			SrcAddr:       &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
			PortMulticast: 1,
		}
	}

	t.Run("missing multicast address", func(t *testing.T) {
		err := validateSyncConfig(newConfig())
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "dst_addr_multicast"))
	})

	t.Run("zero multicast port", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstAddrMulticast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}
		cfg.PortMulticast = 0

		err := validateSyncConfig(cfg)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "port_multicast"))
	})

	t.Run("valid", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstAddrMulticast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}

		err := validateSyncConfig(cfg)
		require.NoError(t, err)
	})
}

// TestUpdateConfigRequiresMapNames checks that a request without both map
// object names is rejected with InvalidArgument naming the missing field,
// before any backend or agent state is touched.
func TestUpdateConfigRequiresMapNames(t *testing.T) {
	syncConfig := &fwstatepb.SyncConfig{
		SrcAddr:          &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
		DstAddrMulticast: &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
		PortMulticast:    9999,
	}

	cases := []struct {
		name       string
		request    *fwstatepb.UpdateConfigRequest
		wantDetail string
	}{
		{
			name: "missing map_name_v4",
			request: &fwstatepb.UpdateConfigRequest{
				Name:       "cfg",
				MapNameV6:  "maps-v6",
				SyncConfig: syncConfig,
			},
			wantDetail: "map_name_v4",
		},
		{
			name: "missing map_name_v6",
			request: &fwstatepb.UpdateConfigRequest{
				Name:       "cfg",
				MapNameV4:  "maps-v4",
				SyncConfig: syncConfig,
			},
			wantDetail: "map_name_v6",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewFWStateService(nil)

			_, err := service.UpdateConfig(t.Context(), tc.request)
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Contains(t, err.Error(), tc.wantDetail)
		})
	}
}

// TestUpdateConfigRejectsUnrepresentableMapNames checks that map names too
// long for the fixed-size C object registry, or containing NUL bytes, are
// rejected before any C state is built: cp_module_link_object would
// silently truncate them and link an unintended map.
func TestUpdateConfigRejectsUnrepresentableMapNames(t *testing.T) {
	syncConfig := &fwstatepb.SyncConfig{
		SrcAddr:          &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
		DstAddrMulticast: &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
		PortMulticast:    9999,
	}

	cases := []struct {
		name   string
		mapV4  string
		mapV6  string
		detail string
	}{
		{
			name:   "v4 name at the C field limit",
			mapV4:  strings.Repeat("a", 80),
			mapV6:  "maps-v6",
			detail: "shorter than 80 bytes",
		},
		{
			name:   "v6 name with embedded NUL",
			mapV4:  "maps-v4",
			mapV6:  "ma\x00ps-v6",
			detail: "must not contain NUL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewFWStateService(nil)

			_, err := service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
				Name:       "cfg",
				MapNameV4:  tc.mapV4,
				MapNameV6:  tc.mapV6,
				SyncConfig: syncConfig,
			})
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Contains(t, err.Error(), tc.detail)
		})
	}
}
