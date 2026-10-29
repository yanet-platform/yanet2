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
	"github.com/yanet-platform/yanet2/controlplane/crud"
	trafgenpb "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
)

// maxFrameLen is the largest replay frame the dataplane can emit.
//
// The dataplane reserves mbuf tailroom with a 16-bit length, so a frame above
// this bound would wrap and corrupt the mbuf. Reject such frames at upload.
const maxFrameLen = 65535

// DeviceState is the published payload: the loaded frames, the target
// rate and the pipeline bindings of the generator, with the frame
// statistics the reads report.
type DeviceState struct {
	RatePps    uint64
	FrameCount uint32
	TotalBytes uint64
	Packets    [][]byte
	Input      []commonpb.Pipeline
	Output     []commonpb.Pipeline
}

// Backend abstracts shared memory operations.
type Backend interface {
	// Update publishes a device config built from the pipelines, frames
	// and rate to the dataplane.
	Update(name string, payload *DeviceState) (crud.Handle, error)
	// Delete removes the named device from the dataplane registry.
	Delete(name string) error
}

// TrafgenService implements the TrafgenService gRPC server.
type TrafgenService struct {
	trafgenpb.UnimplementedTrafgenServiceServer

	core *crud.Core[*DeviceState]
}

// NewTrafgenService constructs a TrafgenService backed by the given Backend.
func NewTrafgenService(backend Backend) *TrafgenService {
	return &TrafgenService{
		core: crud.NewCore(backend),
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
	input := commonpb.PipelinesFromProto(req.GetDevice().GetInput())
	output := commonpb.PipelinesFromProto(req.GetDevice().GetOutput())

	err := m.publish(name, func(current *DeviceState, ok bool) *DeviceState {
		next := &DeviceState{Input: input, Output: output}
		if ok {
			next.Packets = current.Packets
			next.RatePps = current.RatePps
		}
		return next
	})
	if err != nil {
		return nil, err
	}

	return &trafgenpb.UpdateDeviceResponse{}, nil
}

// DeleteDevice unpublishes the named device, removing it from the
// dataplane registry, and releases the handle kept for it along with the
// loaded frames and rate.
//
// The registry delete runs before the entry is withdrawn, so a delete the
// dataplane refuses leaves the published device and its handle in place.
func (m *TrafgenService) DeleteDevice(name string) error {
	return m.core.Delete(name)
}

// ListConfigs returns all known config names of the dataplane instance.
func (m *TrafgenService) ListConfigs(
	ctx context.Context,
	req *trafgenpb.ListConfigsRequest,
) (*trafgenpb.ListConfigsResponse, error) {
	return &trafgenpb.ListConfigsResponse{Configs: m.core.List()}, nil
}

// ShowConfig returns the loaded frame statistics and target rate for a config.
func (m *TrafgenService) ShowConfig(
	ctx context.Context,
	req *trafgenpb.ShowConfigRequest,
) (*trafgenpb.ShowConfigResponse, error) {
	state, ok := m.core.Get(req.GetName())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", req.GetName())
	}

	return &trafgenpb.ShowConfigResponse{
		RatePps:    state.RatePps,
		FrameCount: state.FrameCount,
		TotalBytes: state.TotalBytes,
	}, nil
}

// ShowPackets returns every loaded frame for the named config.
func (m *TrafgenService) ShowPackets(
	ctx context.Context,
	req *trafgenpb.ShowPacketsRequest,
) (*trafgenpb.ShowPacketsResponse, error) {
	state, ok := m.core.Get(req.GetName())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", req.GetName())
	}

	out := make([][]byte, len(state.Packets))
	copy(out, state.Packets)

	return &trafgenpb.ShowPacketsResponse{Packets: out, Truncated: false}, nil
}

// UploadPcap loads the frames to replay from a pcap, preserving the rate and
// the configured pipelines.
func (m *TrafgenService) UploadPcap(
	ctx context.Context,
	req *trafgenpb.UploadPcapRequest,
) (*trafgenpb.UploadPcapResponse, error) {
	name := req.GetName()
	packets, err := parsePcap(req.GetPcap())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse pcap: %v", err)
	}
	if len(packets) == 0 {
		return nil, status.Error(codes.InvalidArgument, "pcap contains no packets")
	}

	err = m.publish(name, func(current *DeviceState, ok bool) *DeviceState {
		next := &DeviceState{Packets: packets}
		if ok {
			next.RatePps = current.RatePps
			next.Input = current.Input
			next.Output = current.Output
		}
		return next
	})
	if err != nil {
		return nil, err
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
	err := m.publish(name, func(current *DeviceState, ok bool) *DeviceState {
		next := &DeviceState{RatePps: req.GetRatePps()}
		if ok {
			next.Packets = current.Packets
			next.Input = current.Input
			next.Output = current.Output
		}
		return next
	})
	if err != nil {
		return nil, err
	}

	return &trafgenpb.SetRateResponse{}, nil
}

// publish builds the next device state from the current one and writes it
// to the dataplane, recording the frame statistics of the frames it
// publishes.
//
// The caller's builder returns the packets, rate and pipelines of the new
// state and may carry any of them over from the current state. A refused
// write is returned as a gRPC status.
func (m *TrafgenService) publish(name string, next func(current *DeviceState, ok bool) *DeviceState) error {
	err := m.core.Mutate(name, func(current *DeviceState, ok bool) (*DeviceState, error) {
		state := next(current, ok)
		state.FrameCount = uint32(len(state.Packets))
		state.TotalBytes = totalBytes(state.Packets)
		return state, nil
	})
	if err != nil {
		return crud.UpdateError(err)
	}

	return nil
}

// totalBytes returns the summed length of the frames.
func totalBytes(packets [][]byte) uint64 {
	var total uint64
	for _, packet := range packets {
		total += uint64(len(packet))
	}
	return total
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
