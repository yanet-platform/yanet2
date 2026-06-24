package trafgen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	trafgenpb "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
)

var errConfigNameRequired = status.Error(codes.InvalidArgument, "config name is required")

// maxFrameLen is the largest replay frame the dataplane can emit.
//
// The dataplane reserves mbuf tailroom with a 16-bit length, so a frame above
// this bound would wrap and corrupt the mbuf; reject such frames at upload.
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
	// The published config is owned by the dataplane's config-generation
	// registry, which reclaims it when the generation is retired; the caller
	// must not free it.
	UpdateDevice(name string, input, output []Pipeline, frames []byte, lengths []uint32, ratePps uint64) error
}

type config struct {
	RatePps    uint64
	FrameCount uint32
	TotalBytes uint64
	Packets    [][]byte
	Input      []Pipeline
	Output     []Pipeline
}

// TrafgenService implements the TrafgenService gRPC server.
type TrafgenService struct {
	trafgenpb.UnimplementedTrafgenServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]*config
}

// NewTrafgenService constructs a TrafgenService backed by the given Backend.
func NewTrafgenService(backend Backend) *TrafgenService {
	return &TrafgenService{
		backend: backend,
		configs: map[string]*config{},
	}
}

// UpdateDevice binds the input/output pipelines of the named generator.
//
// The loaded frames and the target rate are preserved; generated traffic is
// demultiplexed into the configured input pipelines by the dataplane.
func (m *TrafgenService) UpdateDevice(
	ctx context.Context,
	req *trafgenpb.UpdateDeviceRequest,
) (*trafgenpb.UpdateDeviceResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	input := pipelinesFromProto(req.GetDevice().GetInput())
	output := pipelinesFromProto(req.GetDevice().GetOutput())

	m.mu.Lock()
	defer m.mu.Unlock()

	var packets [][]byte
	var ratePps uint64
	if old, ok := m.configs[name]; ok {
		packets = old.Packets
		ratePps = old.RatePps
	}

	if err := m.apply(name, packets, ratePps, input, output); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update device config %q: %v", name, err,
		)
	}

	return &trafgenpb.UpdateDeviceResponse{}, nil
}

// ListConfigs returns all known config names of the dataplane instance.
func (m *TrafgenService) ListConfigs(
	ctx context.Context,
	req *trafgenpb.ListConfigsRequest,
) (*trafgenpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}

	return &trafgenpb.ListConfigsResponse{Configs: names}, nil
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

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.configs[name]
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

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.configs[name]
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

	packets, err := parsePcap(req.GetPcap())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse pcap: %v", err)
	}
	if len(packets) == 0 {
		return nil, status.Error(codes.InvalidArgument, "pcap contains no packets")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var ratePps uint64
	var input, output []Pipeline
	if old, ok := m.configs[name]; ok {
		ratePps = old.RatePps
		input = old.Input
		output = old.Output
	}

	if err := m.apply(name, packets, ratePps, input, output); err != nil {
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

	m.mu.Lock()
	defer m.mu.Unlock()

	var packets [][]byte
	var input, output []Pipeline
	if old, ok := m.configs[name]; ok {
		packets = old.Packets
		input = old.Input
		output = old.Output
	}

	if err := m.apply(name, packets, req.GetRatePps(), input, output); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update device config %q: %v", name, err,
		)
	}

	return &trafgenpb.SetRateResponse{}, nil
}

// apply publishes the full device state through the backend and, on success,
// stores the new config. The caller must hold m.mu.
//
// The previously published device is reclaimed by the dataplane's config
// generation registry, so nothing is freed here.
func (m *TrafgenService) apply(
	name string,
	packets [][]byte,
	ratePps uint64,
	input, output []Pipeline,
) error {
	frames, lengths := flattenFrames(packets)

	if err := m.backend.UpdateDevice(name, input, output, frames, lengths, ratePps); err != nil {
		return fmt.Errorf("failed to update device config %q: %w", name, err)
	}

	m.configs[name] = &config{
		RatePps:    ratePps,
		FrameCount: uint32(len(packets)),
		TotalBytes: uint64(len(frames)),
		Packets:    packets,
		Input:      input,
		Output:     output,
	}

	return nil
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
