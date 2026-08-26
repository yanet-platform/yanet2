package builtin

import (
	"context"
	"errors"
	"math"
	"strconv"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// The most name patterns one request may carry, bounding a client only.
const maxQueryPatterns = 64

// Counters is an in-process gRPC service for retrieving counters.
type Counters struct {
	ynpb.UnimplementedCountersServiceServer

	instanceID uint32
	shm        *ffi.SharedMemory
	log        *zap.Logger
}

// CountersOption configures the Counters constructor.
type CountersOption func(*countersOptions)

type countersOptions struct {
	Log *zap.Logger
}

func newCountersOptions() *countersOptions {
	return &countersOptions{
		Log: zap.NewNop(),
	}
}

// WithCountersLog sets the logger for the counters service.
func WithCountersLog(log *zap.Logger) CountersOption {
	return func(o *countersOptions) {
		o.Log = log
	}
}

// NewCounters creates a new Counters service.
func NewCounters(instanceID uint32, shm *ffi.SharedMemory, options ...CountersOption) *Counters {
	opts := newCountersOptions()
	for _, o := range options {
		o(opts)
	}

	return &Counters{
		instanceID: instanceID,
		shm:        shm,
		log:        opts.Log,
	}
}

// Name returns the service name.
func (m *Counters) Name() string { return "counters" }

// Endpoint returns empty string indicating in-process service.
func (m *Counters) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Counters) ServicesNames() []string { return []string{"controlplane.ynpb.v1.CountersService"} }

// RegisterService registers the service on the given gRPC server.
func (m *Counters) RegisterService(server *grpc.Server) {
	ynpb.RegisterCountersServiceServer(server, m)
}

func (m *Counters) encodeCounters(
	counterValues []ffi.CounterInfo,
) []*ynpb.CounterInfo {
	res := make([]*ynpb.CounterInfo, 0, len(counterValues))

	for _, counter := range counterValues {
		out := &ynpb.CounterInfo{
			Name: counter.Name,
		}

		for iidx := range counter.Values {
			out.Instances = append(
				out.Instances,
				&ynpb.CounterInstanceInfo{
					Values: counter.Values[iidx],
				},
			)
		}

		res = append(res, out)
	}

	return res
}

// Perf returns performance counters.
func (m *Counters) Perf(
	ctx context.Context,
	request *ynpb.PerfCountersRequest,
) (*ynpb.PerfCountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	perfCounters, err := dpConfig.PerformanceCounters(
		request.GetDevice(),
		request.GetPipeline(),
		request.GetFunction(),
		request.GetChain(),
		request.GetModuleType(),
		request.GetModuleName(),
	)
	if err != nil {
		return nil, err
	}

	response := &ynpb.PerfCountersResponse{
		Counters: make([]*ynpb.PerfCounter, 0, len(perfCounters.Counters)),
		Tx:       perfCounters.Tx,
		Rx:       perfCounters.Rx,
		TxBytes:  perfCounters.TxBytes,
		RxBytes:  perfCounters.RxBytes,
	}

	for _, counter := range perfCounters.Counters {
		latencies := make([]*ynpb.LatencyRangeCounter, 0, len(counter.LatencyRanges))
		for _, latencyRange := range counter.LatencyRanges {
			latencies = append(latencies, &ynpb.LatencyRangeCounter{
				MinLatency: uint32(latencyRange.MinLatency),
				Batches:    latencyRange.Batches,
			})
		}

		response.Counters = append(response.Counters, &ynpb.PerfCounter{
			MinBatchSize:   uint32(counter.MinBatchSize),
			SummaryLatency: uint64(counter.SummaryLatency),
			Packets:        uint64(counter.Packets),
			Bytes:          uint64(counter.Bytes),
			Latencies:      latencies,
		})
	}

	return response, nil
}

// ByTags returns counters grouped by tag set, filtered by the request's
// tag predicates and name patterns.
func (m *Counters) ByTags(
	ctx context.Context,
	request *ynpb.CountersByTagsRequest,
) (*ynpb.CountersByTagsResponse, error) {
	if len(request.GetQuery()) > maxQueryPatterns {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"query carries %d patterns, at most %d are accepted",
			len(request.GetQuery()),
			maxQueryPatterns,
		)
	}

	reqTags := request.GetTags()
	tags := make([]ffi.CounterTag, len(reqTags))
	for idx, tag := range reqTags {
		tags[idx] = ffi.CounterTag{
			Key:   tag.GetKey(),
			Value: tag.GetValue(),
		}
	}

	dpConfig := m.shm.DPConfig(m.instanceID)
	groups, err := dpConfig.CountersByTags(tags, request.GetQuery())
	if err != nil {
		if errors.Is(err, ffi.ErrInvalidQuery) ||
			errors.Is(err, ffi.ErrInvalidTag) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, err
	}

	response := &ynpb.CountersByTagsResponse{
		Groups: make([]*ynpb.CounterGroup, 0, len(groups)),
	}
	for _, group := range groups {
		pbTags := make([]*ynpb.CounterTag, 0, len(group.Tags))
		for _, tag := range group.Tags {
			pbTags = append(pbTags, &ynpb.CounterTag{
				Key:   tag.Key,
				Value: tag.Value,
			})
		}
		response.Groups = append(response.Groups, &ynpb.CounterGroup{
			Tags:     pbTags,
			Counters: m.encodeCounters(group.Counters),
		})
	}

	return response, nil
}

// Workers returns raw cumulative worker counters.
func (m *Counters) Workers(
	ctx context.Context,
	request *ynpb.WorkerCountersRequest,
) (*ynpb.WorkerCountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	workers, err := dpConfig.WorkerCounters()
	if err != nil {
		return nil, err
	}

	response := &ynpb.WorkerCountersResponse{
		Workers: make([]*ynpb.WorkerCounter, 0, len(workers)),
	}
	for _, worker := range workers {
		response.Workers = append(response.Workers, &ynpb.WorkerCounter{
			WorkerIdx:       worker.WorkerIdx,
			CoreId:          worker.CoreID,
			DeviceId:        worker.DeviceID,
			QueueId:         worker.QueueID,
			MaxBurstSize:    worker.MaxBurstSize,
			RxBursts:        worker.RxBursts,
			Iterations:      worker.Iterations,
			RxPackets:       worker.RxPackets,
			RxBytes:         worker.RxBytes,
			TxPackets:       worker.TxPackets,
			TxBytes:         worker.TxBytes,
			RemoteRxPackets: worker.RemoteRxPackets,
			RemoteTxPackets: worker.RemoteTxPackets,
			LocalTxDrops:    worker.LocalTxDrops,
			RemoteTxDrops:   worker.RemoteTxDrops,
			Disposed:        worker.Disposed,
		})
	}

	return response, nil
}

func (m *Counters) Ports(
	ctx context.Context,
	request *ynpb.PortCountersRequest,
) (*ynpb.PortCountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	counters, err := dpConfig.PortCounters()
	if err != nil {
		return nil, err
	}

	response := &ynpb.PortCountersResponse{
		Ports: make([]*ynpb.PortCountersGroup, 0, len(counters)),
	}
	for _, group := range counters {
		counterValues := make([]*ynpb.PortCounter, 0, len(group.Counters))
		for _, counter := range group.Counters {
			counterValues = append(counterValues, &ynpb.PortCounter{
				Name:  counter.Name,
				Value: counter.Value,
			})
		}
		response.Ports = append(response.Ports, &ynpb.PortCountersGroup{
			PortId:   uint32(group.PortID),
			PortName: group.PortName,
			Counters: counterValues,
		})
	}

	return response, nil
}

// Collect returns a generic metrics snapshot for port and worker counters.
//
// A family that fails to collect is omitted from the snapshot, so a
// consumer sees a gap for that scrape rather than stale values.
func (m *Counters) Collect() []*commonpb.Metric {
	dpConfig := m.shm.DPConfig(m.instanceID)

	metrics := make([]*commonpb.Metric, 0)

	if ports, err := dpConfig.PortCounters(); err == nil {
		metrics = append(metrics, portMetrics(ports)...)
	} else {
		m.log.Warn("failed to collect port counters", zap.Error(err))
	}

	if workers, err := dpConfig.WorkerCounters(); err == nil {
		metrics = append(metrics, workerMetrics(workers)...)
	} else {
		m.log.Warn("failed to collect worker counters", zap.Error(err))
	}

	return metrics
}

func portMetrics(ports []ffi.PortGroup) []*commonpb.Metric {
	metrics := make([]*commonpb.Metric, 0)
	for _, port := range ports {
		portID := strconv.FormatUint(uint64(port.PortID), 10)
		for _, counter := range port.Counters {
			metrics = append(metrics, commonpb.NewMetricCounter(
				"port_counter_value",
				counter.Value,
				&commonpb.Label{Name: "port_id", Value: portID},
				&commonpb.Label{Name: "port_name", Value: port.PortName},
				&commonpb.Label{Name: "counter", Value: counter.Name},
			))
		}
	}
	return metrics
}

func workerMetrics(workers []ffi.WorkerCounter) []*commonpb.Metric {
	metrics := make([]*commonpb.Metric, 0)
	for _, worker := range workers {
		labels := []*commonpb.Label{
			{Name: "worker_idx", Value: strconv.FormatUint(uint64(worker.WorkerIdx), 10)},
			{Name: "core_id", Value: strconv.FormatUint(uint64(worker.CoreID), 10)},
			{Name: "device_id", Value: strconv.FormatUint(uint64(worker.DeviceID), 10)},
			{Name: "queue_id", Value: strconv.FormatUint(uint64(worker.QueueID), 10)},
		}

		metrics = append(metrics,
			commonpb.NewMetricCounter("worker_iterations", worker.Iterations, labels...),
			commonpb.NewMetricCounter("worker_rx_packets", worker.RxPackets, labels...),
			commonpb.NewMetricCounter("worker_rx_bytes", worker.RxBytes, labels...),
			commonpb.NewMetricCounter("worker_tx_packets", worker.TxPackets, labels...),
			commonpb.NewMetricCounter("worker_tx_bytes", worker.TxBytes, labels...),
			commonpb.NewMetricCounter("worker_remote_rx_packets", worker.RemoteRxPackets, labels...),
			commonpb.NewMetricCounter("worker_remote_tx_packets", worker.RemoteTxPackets, labels...),
			commonpb.NewMetricCounter("worker_local_tx_drops", worker.LocalTxDrops, labels...),
			commonpb.NewMetricCounter("worker_remote_tx_drops", worker.RemoteTxDrops, labels...),
			commonpb.NewMetricCounter("worker_disposed", worker.Disposed, labels...),
		)

		if len(worker.RxBursts) > 0 {
			metrics = append(metrics, makeHistogram(
				"worker_rx_bursts",
				worker.RxBursts,
				labels...,
			))
		}
	}
	return metrics
}

// makeHistogram builds a histogram metric from a slice of raw per-bucket
// counts. The bucket at index i holds the number of RX polls that returned
// exactly i packets, so its upper bound is i (packets per burst).
func makeHistogram(name string, counts []uint64, labels ...*commonpb.Label) *commonpb.Metric {
	buckets := make([]*commonpb.Bucket, 0, len(counts)+1)
	var totalCount uint64
	for i, count := range counts {
		totalCount += count
		buckets = append(buckets, &commonpb.Bucket{
			Count:      count,
			UpperBound: float64(i),
		})
	}
	buckets = append(buckets, &commonpb.Bucket{
		Count:      0,
		UpperBound: math.Inf(1),
	})

	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value: &commonpb.Metric_Histogram{
			Histogram: &commonpb.Histogram{
				Buckets:    buckets,
				TotalCount: totalCount,
			},
		},
	}
}
