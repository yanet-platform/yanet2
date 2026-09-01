package trafgen

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/trafgen/bindings/go/ctrafgen"
	trafgenpb "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
)

// recordingBackend captures the arguments of the most recent UpdateDevice call.
type recordingBackend struct {
	frames  []byte
	lengths []uint32
	ratePps uint64
	input   []Pipeline
	output  []Pipeline
	calls   int
}

func (m *recordingBackend) UpdateDevice(
	name string,
	input, output []Pipeline,
	frames []byte,
	lengths []uint32,
	ratePps uint64,
) (*ctrafgen.DeviceConfig, error) {
	m.calls++
	m.frames = frames
	m.lengths = lengths
	m.ratePps = ratePps
	m.input = input
	m.output = output
	return nil, nil
}

// buildPcap encodes the given ethernet frames into an in-memory pcap file.
func buildPcap(t *testing.T, frames [][]byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := pcapgo.NewWriter(&buf)
	require.NoError(t, writer.WriteFileHeader(65535, layers.LinkTypeEthernet))

	for _, frame := range frames {
		ci := gopacket.CaptureInfo{
			Timestamp:     time.Unix(0, 0),
			CaptureLength: len(frame),
			Length:        len(frame),
		}
		require.NoError(t, writer.WritePacket(ci, frame))
	}

	return buf.Bytes()
}

func newTestService() (*TrafgenService, *recordingBackend) {
	backend := &recordingBackend{}
	return NewTrafgenService(backend), backend
}

func Test_TrafgenService_UploadSetRateShow(t *testing.T) {
	svc, backend := newTestService()
	ctx := t.Context()

	frames := [][]byte{
		bytes.Repeat([]byte{0xaa}, 20),
		bytes.Repeat([]byte{0xbb}, 30),
	}

	_, err := svc.UploadPcap(ctx, &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buildPcap(t, frames),
	})
	require.NoError(t, err)
	require.Equal(t, []uint32{20, 30}, backend.lengths)
	require.Len(t, backend.frames, 50)

	_, err = svc.SetRate(ctx, &trafgenpb.SetRateRequest{
		Name:    "trafgen0",
		RatePps: 1_000_000,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1_000_000), backend.ratePps)

	show, err := svc.ShowConfig(ctx, &trafgenpb.ShowConfigRequest{Name: "trafgen0"})
	require.NoError(t, err)
	require.Equal(t, uint64(1_000_000), show.RatePps)
	require.Equal(t, uint32(2), show.FrameCount)
	require.Equal(t, uint64(50), show.TotalBytes)
}

func Test_TrafgenService_SetRateKeepsPcap(t *testing.T) {
	svc, backend := newTestService()
	ctx := t.Context()

	_, err := svc.UploadPcap(ctx, &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buildPcap(t, [][]byte{bytes.Repeat([]byte{0x01}, 64)}),
	})
	require.NoError(t, err)

	// Changing the rate must republish the same frames without a re-upload.
	_, err = svc.SetRate(ctx, &trafgenpb.SetRateRequest{Name: "trafgen0", RatePps: 500})
	require.NoError(t, err)
	require.Equal(t, []uint32{64}, backend.lengths)
	require.Len(t, backend.frames, 64)
	require.Equal(t, uint64(500), backend.ratePps)

	show, err := svc.ShowConfig(ctx, &trafgenpb.ShowConfigRequest{Name: "trafgen0"})
	require.NoError(t, err)
	require.Equal(t, uint32(1), show.FrameCount)
	require.Equal(t, uint64(500), show.RatePps)
}

func Test_TrafgenService_UpdateDeviceKeepsFramesAndRate(t *testing.T) {
	svc, backend := newTestService()
	ctx := t.Context()

	_, err := svc.UploadPcap(ctx, &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buildPcap(t, [][]byte{bytes.Repeat([]byte{0x01}, 64)}),
	})
	require.NoError(t, err)

	_, err = svc.SetRate(ctx, &trafgenpb.SetRateRequest{Name: "trafgen0", RatePps: 700})
	require.NoError(t, err)

	// Binding pipelines must republish the same frames and rate.
	_, err = svc.UpdateDevice(ctx, &trafgenpb.UpdateDeviceRequest{
		Name: "trafgen0",
		Device: &commonpb.Device{
			Input:  []*commonpb.DevicePipeline{{Name: "p0", Weight: 1}},
			Output: []*commonpb.DevicePipeline{{Name: "p1", Weight: 2}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []Pipeline{{Name: "p0", Weight: 1}}, backend.input)
	require.Equal(t, []Pipeline{{Name: "p1", Weight: 2}}, backend.output)
	require.Len(t, backend.frames, 64)
	require.Equal(t, uint64(700), backend.ratePps)

	// A later rate change must preserve the bound pipelines.
	_, err = svc.SetRate(ctx, &trafgenpb.SetRateRequest{Name: "trafgen0", RatePps: 900})
	require.NoError(t, err)
	require.Equal(t, []Pipeline{{Name: "p0", Weight: 1}}, backend.input)
	require.Equal(t, []Pipeline{{Name: "p1", Weight: 2}}, backend.output)
}

func Test_TrafgenService_SetRateBeforeUpload(t *testing.T) {
	svc, _ := newTestService()
	ctx := t.Context()

	_, err := svc.SetRate(ctx, &trafgenpb.SetRateRequest{Name: "trafgen0", RatePps: 42})
	require.NoError(t, err)

	show, err := svc.ShowConfig(ctx, &trafgenpb.ShowConfigRequest{Name: "trafgen0"})
	require.NoError(t, err)
	require.Equal(t, uint64(42), show.RatePps)
	require.Equal(t, uint32(0), show.FrameCount)

	packets, err := svc.ShowPackets(ctx, &trafgenpb.ShowPacketsRequest{Name: "trafgen0"})
	require.NoError(t, err)
	require.Empty(t, packets.Packets)
}

func Test_TrafgenService_ShowPackets(t *testing.T) {
	svc, _ := newTestService()
	ctx := t.Context()

	frames := [][]byte{
		bytes.Repeat([]byte{0xaa}, 20),
		bytes.Repeat([]byte{0xbb}, 30),
	}
	_, err := svc.UploadPcap(ctx, &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buildPcap(t, frames),
	})
	require.NoError(t, err)

	resp, err := svc.ShowPackets(ctx, &trafgenpb.ShowPacketsRequest{Name: "trafgen0"})
	require.NoError(t, err)
	require.False(t, resp.Truncated)
	require.Equal(t, frames, resp.Packets)
}

func Test_TrafgenService_UploadReplaces(t *testing.T) {
	svc, backend := newTestService()
	ctx := t.Context()

	_, err := svc.UploadPcap(ctx, &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buildPcap(t, [][]byte{bytes.Repeat([]byte{0x01}, 64)}),
	})
	require.NoError(t, err)

	_, err = svc.UploadPcap(ctx, &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buildPcap(t, [][]byte{bytes.Repeat([]byte{0x02}, 100), bytes.Repeat([]byte{0x03}, 100)}),
	})
	require.NoError(t, err)

	require.Equal(t, 2, backend.calls)

	show, err := svc.ShowConfig(ctx, &trafgenpb.ShowConfigRequest{Name: "trafgen0"})
	require.NoError(t, err)
	require.Equal(t, uint32(2), show.FrameCount)
	require.Equal(t, uint64(200), show.TotalBytes)
}

func Test_TrafgenService_ListConfigs(t *testing.T) {
	svc, _ := newTestService()
	ctx := t.Context()

	list, err := svc.ListConfigs(ctx, &trafgenpb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Empty(t, list.Configs)

	_, err = svc.UploadPcap(ctx, &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buildPcap(t, [][]byte{bytes.Repeat([]byte{0x01}, 64)}),
	})
	require.NoError(t, err)

	list, err = svc.ListConfigs(ctx, &trafgenpb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"trafgen0"}, list.Configs)
}

func Test_TrafgenService_EmptyConfigName(t *testing.T) {
	svc, _ := newTestService()
	ctx := t.Context()

	t.Run("UpdateDevice", func(t *testing.T) {
		resp, err := svc.UpdateDevice(ctx, &trafgenpb.UpdateDeviceRequest{
			Device: &commonpb.Device{},
		})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("UploadPcap", func(t *testing.T) {
		resp, err := svc.UploadPcap(ctx, &trafgenpb.UploadPcapRequest{
			Pcap: buildPcap(t, [][]byte{bytes.Repeat([]byte{0x01}, 64)}),
		})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetRate", func(t *testing.T) {
		resp, err := svc.SetRate(ctx, &trafgenpb.SetRateRequest{RatePps: 1})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ShowConfig", func(t *testing.T) {
		resp, err := svc.ShowConfig(ctx, &trafgenpb.ShowConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ShowPackets", func(t *testing.T) {
		resp, err := svc.ShowPackets(ctx, &trafgenpb.ShowPacketsRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

// Test_TrafgenService_UpdateDevice_RejectsOverlongName verifies that a name
// the C-side fixed-size buffer cannot hold is rejected before the backend.
func Test_TrafgenService_UpdateDevice_RejectsOverlongName(t *testing.T) {
	svc, backend := newTestService()

	resp, err := svc.UpdateDevice(t.Context(), &trafgenpb.UpdateDeviceRequest{
		Name:   strings.Repeat("a", ffi.MaxDeviceNameLen),
		Device: &commonpb.Device{},
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Zero(t, backend.calls)
}

// Test_TrafgenService_UpdateDevice_AcceptsNameAtLimit verifies that a name
// exactly at the C-side buffer's usable length reaches the backend.
func Test_TrafgenService_UpdateDevice_AcceptsNameAtLimit(t *testing.T) {
	svc, backend := newTestService()

	_, err := svc.UpdateDevice(t.Context(), &trafgenpb.UpdateDeviceRequest{
		Name:   strings.Repeat("a", ffi.MaxDeviceNameLen-1),
		Device: &commonpb.Device{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, backend.calls)
}

// Test_TrafgenService_UploadPcap_RejectsOverlongName verifies that an overlong
// name is rejected before the backend even when no config exists yet.
func Test_TrafgenService_UploadPcap_RejectsOverlongName(t *testing.T) {
	svc, backend := newTestService()

	resp, err := svc.UploadPcap(t.Context(), &trafgenpb.UploadPcapRequest{
		Name: strings.Repeat("a", ffi.MaxDeviceNameLen),
		Pcap: buildPcap(t, [][]byte{bytes.Repeat([]byte{0x01}, 64)}),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Zero(t, backend.calls)
}

// Test_TrafgenService_SetRate_RejectsOverlongName verifies that an overlong
// name is rejected before the backend even when no config exists yet.
func Test_TrafgenService_SetRate_RejectsOverlongName(t *testing.T) {
	svc, backend := newTestService()

	resp, err := svc.SetRate(t.Context(), &trafgenpb.SetRateRequest{
		Name:    strings.Repeat("a", ffi.MaxDeviceNameLen),
		RatePps: 1,
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Zero(t, backend.calls)
}

func Test_TrafgenService_InvalidPcap(t *testing.T) {
	svc, _ := newTestService()

	resp, err := svc.UploadPcap(t.Context(), &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: []byte("not a pcap file"),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func Test_TrafgenService_OversizedFrame(t *testing.T) {
	svc, _ := newTestService()

	var buf bytes.Buffer
	writer := pcapgo.NewWriter(&buf)
	require.NoError(t, writer.WriteFileHeader(maxFrameLen+1, layers.LinkTypeEthernet))

	frame := bytes.Repeat([]byte{0x01}, maxFrameLen+1)
	require.NoError(t, writer.WritePacket(gopacket.CaptureInfo{
		Timestamp:     time.Unix(0, 0),
		CaptureLength: len(frame),
		Length:        len(frame),
	}, frame))

	resp, err := svc.UploadPcap(t.Context(), &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buf.Bytes(),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func Test_TrafgenService_EmptyPcap(t *testing.T) {
	svc, _ := newTestService()

	resp, err := svc.UploadPcap(t.Context(), &trafgenpb.UploadPcapRequest{
		Name: "trafgen0",
		Pcap: buildPcap(t, nil),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
