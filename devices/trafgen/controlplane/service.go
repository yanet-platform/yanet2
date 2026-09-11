package trafgen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/trafgen/bindings/go/ctrafgen"
	trafgenpb "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
)

var errConfigNameRequired = status.Error(codes.InvalidArgument, "config name is required")

// maxFrameLen is the largest replay frame the dataplane can emit.
//
// The dataplane reserves mbuf tailroom with a 16-bit length, so a frame above
// this bound would wrap and corrupt the mbuf. Reject such frames at upload.
const maxFrameLen = 65535

// Pipeline is a weighted input/output pipeline assignment for the generator.
type Pipeline struct {
	Name   string
	Weight uint64
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateDevice publishes a device config with the given pipelines,
	// frames and rate to the dataplane.
	//
	// It returns the freshly published handle. The caller owns the handle
	// and must Free it once a newer generation has superseded it.
	UpdateDevice(name string, input, output []Pipeline, frames []byte, lengths []uint32, ratePps uint64) (*ctrafgen.DeviceConfig, error)
}

type config struct {
	RatePps    uint64
	FrameCount uint32
	TotalBytes uint64
	Packets    [][]byte
	Input      []Pipeline
	Output     []Pipeline
	Handle     *ctrafgen.DeviceConfig
}

// Free releases the device handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *config) Free() error {
	if m.Handle == nil {
		return nil
	}
	return m.Handle.Free()
}

// TrafgenService implements the TrafgenService gRPC server.
type TrafgenService struct {
	trafgenpb.UnimplementedTrafgenServiceServer

	backend Backend
	configs *configstore.Store[*config]
}

// NewTrafgenService constructs a TrafgenService backed by the given Backend.
func NewTrafgenService(backend Backend) *TrafgenService {
	return &TrafgenService{
		backend: backend,
		configs: configstore.NewStore[*config](),
	}
}

// UpdateDevice binds the input/output pipelines of the named generator.
//
// The loaded frames and the target rate are preserved. Generated traffic is
// demultiplexed into the configured input pipelines by the dataplane.
func (m *TrafgenService) UpdateDevice(
	ctx context.Context,
	req *trafgenpb.UpdateDeviceRequest,
) (*trafgenpb.UpdateDeviceResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}
	if err := ffi.ValidateDeviceName(name); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := req.GetDevice().Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	input := pipelinesFromProto(req.GetDevice().GetInput())
	output := pipelinesFromProto(req.GetDevice().GetOutput())

	err := m.publish(name, func(current *config, ok bool) *config {
		next := &config{Input: input, Output: output}
		if ok {
			next.Packets = current.Packets
			next.RatePps = current.RatePps
		}
		return next
	})
	if err != nil {
		code := codes.Internal
		if errors.Is(err, ffi.ErrFailedPrecondition) {
			// The device names an entity of the graph it runs that the
			// configuration cannot resolve.
			code = codes.FailedPrecondition
		}
		return nil, status.Errorf(code, "failed to update device config %q: %v", name, err)
	}

	return &trafgenpb.UpdateDeviceResponse{}, nil
}

// ListConfigs returns all known config names of the dataplane instance.
func (m *TrafgenService) ListConfigs(
	ctx context.Context,
	req *trafgenpb.ListConfigsRequest,
) (*trafgenpb.ListConfigsResponse, error) {
	return &trafgenpb.ListConfigsResponse{Configs: m.configs.Names()}, nil
}

// ShowConfig returns the loaded frame statistics and target rate for a config.
func (m *TrafgenService) ShowConfig(
	ctx context.Context,
	req *trafgenpb.ShowConfigRequest,
) (*trafgenpb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	entry, ok := m.configs.Get(name)
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	return &trafgenpb.ShowConfigResponse{
		RatePps:    entry.RatePps,
		FrameCount: entry.FrameCount,
		TotalBytes: entry.TotalBytes,
	}, nil
}

// ShowPackets returns every loaded frame for the named config.
func (m *TrafgenService) ShowPackets(
	ctx context.Context,
	req *trafgenpb.ShowPacketsRequest,
) (*trafgenpb.ShowPacketsResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	entry, ok := m.configs.Get(name)
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	out := make([][]byte, len(entry.Packets))
	copy(out, entry.Packets)

	return &trafgenpb.ShowPacketsResponse{Packets: out, Truncated: false}, nil
}

// UploadPcap loads the frames to replay from a pcap, preserving the rate and
// the configured pipelines.
func (m *TrafgenService) UploadPcap(
	ctx context.Context,
	req *trafgenpb.UploadPcapRequest,
) (*trafgenpb.UploadPcapResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}
	if err := ffi.ValidateDeviceName(name); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	packets, err := parsePcap(req.GetPcap())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse pcap: %v", err)
	}
	if len(packets) == 0 {
		return nil, status.Error(codes.InvalidArgument, "pcap contains no packets")
	}

	err = m.publish(name, func(current *config, ok bool) *config {
		next := &config{Packets: packets}
		if ok {
			next.RatePps = current.RatePps
			next.Input = current.Input
			next.Output = current.Output
		}
		return next
	})
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update device config %q: %v", name, err,
		)
	}

	return &trafgenpb.UploadPcapResponse{}, nil
}

// SetRate sets the target aggregate rate, preserving the loaded pcap and the
// configured pipelines.
func (m *TrafgenService) SetRate(
	ctx context.Context,
	req *trafgenpb.SetRateRequest,
) (*trafgenpb.SetRateResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}
	if err := ffi.ValidateDeviceName(name); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	err := m.publish(name, func(current *config, ok bool) *config {
		next := &config{RatePps: req.GetRatePps()}
		if ok {
			next.Packets = current.Packets
			next.Input = current.Input
			next.Output = current.Output
		}
		return next
	})
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update device config %q: %v", name, err,
		)
	}

	return &trafgenpb.SetRateResponse{}, nil
}

// publish builds the next device state from the current one and writes it
// to the dataplane, attaching the published handle and frame statistics.
//
// The caller's builder returns the packets, rate and pipelines of the new
// state and may carry any of them over from the current config.
func (m *TrafgenService) publish(name string, next func(current *config, ok bool) *config) error {
	return m.configs.Update(name, func(current *config, ok bool) (*config, error) {
		cfg := next(current, ok)
		frames, lengths := flattenFrames(cfg.Packets)

		handle, err := m.backend.UpdateDevice(name, cfg.Input, cfg.Output, frames, lengths, cfg.RatePps)
		if err != nil {
			return nil, fmt.Errorf("failed to update device config %q: %w", name, err)
		}

		cfg.FrameCount = uint32(len(cfg.Packets))
		cfg.TotalBytes = uint64(len(frames))
		cfg.Handle = handle

		return cfg, nil
	})
}

// pipelinesFromProto converts the wire pipeline assignments into the service
// representation.
func pipelinesFromProto(pipelines []*commonpb.DevicePipeline) []Pipeline {
	out := make([]Pipeline, 0, len(pipelines))
	for _, pipeline := range pipelines {
		out = append(out, Pipeline{
			Name:   pipeline.GetName(),
			Weight: pipeline.GetWeight(),
		})
	}
	return out
}

// flattenFrames concatenates the per-packet frames into a single buffer and
// returns it together with the per-frame lengths.
func flattenFrames(packets [][]byte) ([]byte, []uint32) {
	if len(packets) == 0 {
		return nil, nil
	}

	lengths := make([]uint32, len(packets))
	total := 0
	for idx, packet := range packets {
		lengths[idx] = uint32(len(packet))
		total += len(packet)
	}

	frames := make([]byte, 0, total)
	for _, packet := range packets {
		frames = append(frames, packet...)
	}

	return frames, lengths
}

// parsePcap reads every packet from a pcap file's raw bytes, returning the
// list of raw L2 frames in capture order.
func parsePcap(pcap []byte) ([][]byte, error) {
	reader, err := pcapgo.NewReader(bytes.NewReader(pcap))
	if err != nil {
		return nil, fmt.Errorf("failed to read pcap header: %w", err)
	}

	if linkType := reader.LinkType(); linkType != layers.LinkTypeEthernet {
		return nil, fmt.Errorf("unsupported link type %s: only ethernet is supported", linkType)
	}

	var packets [][]byte
	for {
		data, _, err := reader.ReadPacketData()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read packet: %w", err)
		}
		if len(data) > maxFrameLen {
			return nil, fmt.Errorf(
				"frame %d is %d bytes, exceeds the %d-byte limit",
				len(packets), len(data), maxFrameLen,
			)
		}
		frame := make([]byte, len(data))
		copy(frame, data)
		packets = append(packets, frame)
	}

	return packets, nil
}

// ReclaimDeferred retries every superseded device whose free was refused,
// releasing the ones whose generations have drained.
//
// The service runs it after each successful publish, and anything else
// may call it at any time.
func (m *TrafgenService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}
