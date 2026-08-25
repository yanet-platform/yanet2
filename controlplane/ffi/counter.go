package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../build/lib/errors -lerrors
//#include "api/agent.h"
//#include "api/counter.h"
import "C"

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
)

// CounterInfo is a snapshot of one dataplane counter sharded across equally sized instances.
type CounterInfo struct {
	Name string
	// Values is indexed by instance, then by value index within that instance.
	Values [][]uint64
}

// Instances returns the number of instances this counter is sharded across.
func (m CounterInfo) Instances() int {
	return len(m.Values)
}

// Size returns the number of values each instance holds.
func (m CounterInfo) Size() int {
	if m.Instances() == 0 {
		return 0
	}
	return len(m.Values[0])
}

// Value returns one value of one instance.
func (m CounterInfo) Value(instance int, idx int) uint64 {
	values := m.InstanceValues(instance)
	return values[idx]
}

// InstanceValues returns every value of one instance.
func (m CounterInfo) InstanceValues(instance int) []uint64 {
	return m.Values[instance]
}

func decodeCounterHandle(
	handle *C.struct_counter_handle,
	instanceCount C.uint64_t,
) CounterInfo {
	size := int(handle.size)
	instances := int(instanceCount)

	values := make([][]uint64, instances)
	if total := instances * size; total > 0 {
		flat := make([]uint64, total)
		C.yanet_get_counter_values(
			handle.values,
			handle.size,
			instanceCount,
			(*C.uint64_t)(unsafe.Pointer(&flat[0])),
		)
		for iidx := range instances {
			values[iidx] = flat[iidx*size : (iidx+1)*size : (iidx+1)*size]
		}
	}

	return CounterInfo{
		Name:   C.GoString(&handle.name[0]),
		Values: values,
	}
}

func (m *DPConfig) encodeCounters(
	counters *C.struct_counter_handle_list,
) []CounterInfo {
	res := make([]CounterInfo, 0, counters.count)

	for cidx := C.uint64_t(0); cidx < counters.count; cidx++ {
		handle := C.yanet_get_counter(counters, cidx)
		if handle != nil {
			res = append(res, decodeCounterHandle(handle, counters.instance_count))
		}
	}

	return res
}

func (m *DPConfig) DeviceCounters(
	deviceName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	counters := C.yanet_get_device_counters(m.ptr, cDeviceName)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

// PipelineCounters returns pipeline counters
func (m *DPConfig) PipelineCounters(
	deviceName string,
	pipelineName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	counters := C.yanet_get_pipeline_counters(m.ptr, cDeviceName, cPipelineName)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

func (m *DPConfig) FunctionCounters(
	deviceName string,
	pipelineName string,
	functionName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	cFunctionName := C.CString(functionName)
	defer C.free(unsafe.Pointer(cFunctionName))
	counters := C.yanet_get_function_counters(
		m.ptr,
		cDeviceName,
		cPipelineName,
		cFunctionName,
	)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

func (m *DPConfig) ChainCounters(
	deviceName string,
	pipelineName string,
	functionName string,
	chainName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	cFunctionName := C.CString(functionName)
	defer C.free(unsafe.Pointer(cFunctionName))
	cChainName := C.CString(chainName)
	defer C.free(unsafe.Pointer(cChainName))
	counters := C.yanet_get_chain_counters(
		m.ptr,
		cDeviceName,
		cPipelineName,
		cFunctionName,
		cChainName,
	)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

// ModuleCounters returns module counters, optionally filtered by name.
//
// If counterQuery is nil or empty, returns all counters, a refused one none.
func (m *DPConfig) ModuleCounters(
	deviceName string,
	pipelineName string,
	functionName string,
	chainName string,
	moduleType string,
	moduleName string,
	counterQuery []string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	cFunctionName := C.CString(functionName)
	defer C.free(unsafe.Pointer(cFunctionName))
	cChainName := C.CString(chainName)
	defer C.free(unsafe.Pointer(cChainName))
	cModuleType := C.CString(moduleType)
	defer C.free(unsafe.Pointer(cModuleType))
	cModuleName := C.CString(moduleName)
	defer C.free(unsafe.Pointer(cModuleName))

	if ValidateQuery(counterQuery) != nil {
		return nil
	}

	var query *C.struct_counter_query
	if len(counterQuery) > 0 {
		cQuery := make([]*C.char, len(counterQuery))
		for idx, name := range counterQuery {
			cQuery[idx] = C.CString(name)
		}
		defer func() {
			for _, ptr := range cQuery {
				C.free(unsafe.Pointer(ptr))
			}
		}()

		if C.yanet_counter_query_compile(
			&cQuery[0],
			C.size_t(len(cQuery)),
			&query,
			nil,
		) != C.YANET_COUNTER_QUERY_OK {
			return nil
		}
		defer C.yanet_counter_query_free(query)
	}

	counters := C.yanet_get_module_counters(
		m.ptr,
		cDeviceName,
		cPipelineName,
		cFunctionName,
		cChainName,
		cModuleType,
		cModuleName,
		query,
	)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

// ModuleRuntimeCounters returns the module's runtime counters — those
// expanded from its named counter registries (per-route, per-rule, etc.) —
// optionally filtered by name.
//
// If counterQuery is nil or empty, returns all runtime counters.
func (m *DPConfig) ModuleRuntimeCounters(
	deviceName string,
	pipelineName string,
	functionName string,
	chainName string,
	moduleType string,
	moduleName string,
	counterQuery []string,
) ([]CounterInfo, error) {
	groups, err := m.CountersByTags([]CounterTag{
		{Key: "device", Value: deviceName},
		{Key: "pipeline", Value: pipelineName},
		{Key: "function", Value: functionName},
		{Key: "chain", Value: chainName},
		{Key: "module_type", Value: moduleType},
		{Key: "module_name", Value: moduleName},
		{Key: "kind", Value: "runtime"},
	}, counterQuery)
	if err != nil {
		return nil, err
	}

	counters := make([]CounterInfo, 0)
	for _, group := range groups {
		counters = append(counters, group.Counters...)
	}
	return counters, nil
}

// ObjectCounters returns the counters of an object identified by its type
// and name.
func (m *DPConfig) ObjectCounters(
	objectType string,
	objectName string,
) []CounterInfo {
	cObjectType := C.CString(objectType)
	defer C.free(unsafe.Pointer(cObjectType))
	cObjectName := C.CString(objectName)
	defer C.free(unsafe.Pointer(cObjectName))
	counters := C.yanet_get_object_counters(m.ptr, cObjectType, cObjectName)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

// RawWorkerCounters returns the worker counters, or nil when they cannot be read.
func (m *DPConfig) RawWorkerCounters() []CounterInfo {
	counters := C.yanet_get_worker_counters(m.ptr)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

// CounterTag is a (key, value) predicate against a counter's tag set.
// Value semantics follow the C API: an empty string requires the tag to
// be absent, "*" requires the tag to be present with any value, and any
// other string requires an exact match.
type CounterTag struct {
	Key   string
	Value string
}

// ErrInvalidQuery reports a counter query the matcher refused to compile.
var ErrInvalidQuery = errors.New("invalid counter query")

// ValidateQuery rejects a query a C string cannot carry, such as one with a NUL.
func ValidateQuery(query []string) error {
	for idx, pattern := range query {
		if strings.ContainsRune(pattern, 0) {
			return fmt.Errorf(
				"%w: pattern %d contains a NUL byte",
				ErrInvalidQuery,
				idx,
			)
		}
	}

	return nil
}

// CounterGroup is a set of counters that share the same tag set.
type CounterGroup struct {
	Tags     []CounterTag
	Counters []CounterInfo
}

// CountersByTags returns counters matching every predicate in tags and at
// least one pattern in query. A nil or empty tags slice imposes no per-tag
// constraint. A nil or empty query matches any counter name.
//
// Every worker's counter storage registry is matched independently and the
// sets are merged: a counter is identified by its group's tag set and its
// name, and a worker that does not carry it contributes zero values for
// its instance. Every returned counter therefore spans exactly one value
// set per dataplane worker.
func (m *DPConfig) CountersByTags(
	tags []CounterTag,
	query []string,
) ([]CounterGroup, error) {
	if err := ValidateQuery(query); err != nil {
		return nil, err
	}

	cTags := make([]C.struct_counter_tag, len(tags))
	for idx, tag := range tags {
		cKey := C.CString(tag.Key)
		cValue := C.CString(tag.Value)
		defer C.free(unsafe.Pointer(cKey))
		defer C.free(unsafe.Pointer(cValue))
		cTags[idx] = C.struct_counter_tag{key: cKey, value: cValue}
	}

	cQuery := make([]*C.char, len(query))
	for idx, name := range query {
		cQuery[idx] = C.CString(name)
		defer C.free(unsafe.Pointer(cQuery[idx]))
	}

	var cTagsPtr *C.struct_counter_tag
	if len(cTags) > 0 {
		cTagsPtr = &cTags[0]
	}
	var compiled *C.struct_counter_query
	if len(cQuery) > 0 {
		var cQueryErr *C.yanet_error
		res := C.yanet_counter_query_compile(
			&cQuery[0],
			C.size_t(len(cQuery)),
			&compiled,
			&cQueryErr,
		)
		if res != C.YANET_COUNTER_QUERY_OK {
			err := cerrors.FromC(unsafe.Pointer(cQueryErr))
			if res == C.YANET_COUNTER_QUERY_REJECTED {
				return nil, fmt.Errorf(
					"%w: %w", ErrInvalidQuery, err,
				)
			}

			return nil, err
		}
		defer C.yanet_counter_query_free(compiled)
	}

	var cErr *C.yanet_error
	sets := C.yanet_get_counters_by_tags_per_worker(
		m.ptr,
		cTagsPtr,
		C.size_t(len(cTags)),
		compiled,
		&cErr,
	)
	if sets == nil {
		if err := cerrors.FromC(unsafe.Pointer(cErr)); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unknown error")
	}
	defer C.yanet_counter_worker_set_list_free(sets)

	workerCount := int(sets.worker_count)
	perWorker := make([][]CounterGroup, workerCount)
	for idx := range workerCount {
		set := C.yanet_get_counter_worker_set(sets, C.uint64_t(idx))
		if set == nil {
			return nil, fmt.Errorf("counter set for worker %d is missing", idx)
		}
		perWorker[idx] = decodeCounterGroups(set.counters)
	}

	return MergeWorkerCounterGroups(workerCount, perWorker), nil
}

// decodeCounterGroups splits one handle list into groups of counters
// sharing the same tag set. Consecutive handles of one storage share the
// tags pointer, which marks the group boundary. A nil list decodes to no
// groups.
func decodeCounterGroups(counters *C.struct_counter_handle_list) []CounterGroup {
	groups := make([]CounterGroup, 0)
	if counters == nil {
		return groups
	}

	for idx := C.uint64_t(0); idx < counters.count; idx++ {
		handle := C.yanet_get_counter(counters, idx)

		if idx == 0 || C.yanet_get_counter(counters, idx-1).tags != handle.tags {
			tags := decodeCounterTags(handle.tags, handle.tag_count)
			groups = append(groups, CounterGroup{Tags: tags})
		}

		group := &groups[len(groups)-1]
		group.Counters = append(
			group.Counters,
			decodeCounterHandle(handle, counters.instance_count))
	}

	return groups
}

// counterGroupKey builds a canonical identity of a tag set, independent
// of the tags' order.
func counterGroupKey(tags []CounterTag) string {
	ordered := slices.Clone(tags)
	slices.SortFunc(ordered, func(a, b CounterTag) int {
		return strings.Compare(a.Key, b.Key)
	})

	var key strings.Builder
	for _, tag := range ordered {
		key.WriteString(tag.Key)
		key.WriteByte(0)
		key.WriteString(tag.Value)
		key.WriteByte(1)
	}
	return key.String()
}

// MergeWorkerCounterGroups combines per-worker counter groups into one
// slice spanning every worker.
//
// A counter is identified by its group's tag set and its name. The first
// worker carrying it defines its position and its size, every other
// carrier's snapshot lands in that worker's instance slot, and workers
// without the counter contribute zero values in theirs. Sizes come from
// the module's registry and are expected to agree across workers; on
// disagreement the first worker's size stays and the disagreeing worker's
// instance remains zero.
func MergeWorkerCounterGroups(
	workerCount int,
	perWorker [][]CounterGroup,
) []CounterGroup {
	type mergedGroup struct {
		tags     []CounterTag
		counters []CounterInfo
		sizes    []int
		byName   map[string]int
	}

	groups := make([]*mergedGroup, 0)
	groupIndex := map[string]int{}

	for workerIdx, workerGroups := range perWorker {
		for _, group := range workerGroups {
			key := counterGroupKey(group.Tags)
			idx, found := groupIndex[key]
			if !found {
				idx = len(groups)
				groupIndex[key] = idx
				groups = append(groups, &mergedGroup{
					tags:   group.Tags,
					byName: map[string]int{},
				})
			}
			merged := groups[idx]
			for _, counter := range group.Counters {
				size := 0
				if len(counter.Values) > 0 {
					size = len(counter.Values[0])
				}

				counterIdx, found := merged.byName[counter.Name]
				if !found {
					counterIdx = len(merged.counters)
					merged.byName[counter.Name] = counterIdx
					merged.counters = append(
						merged.counters,
						CounterInfo{
							Name:   counter.Name,
							Values: make([][]uint64, workerCount),
						},
					)
					merged.sizes = append(merged.sizes, size)
				}

				if size != merged.sizes[counterIdx] {
					continue
				}
				if len(counter.Values) > 0 {
					merged.counters[counterIdx].Values[workerIdx] = counter.Values[0]
				}
			}
		}
	}

	merged := make([]CounterGroup, 0, len(groups))
	for _, group := range groups {
		for idx, counter := range group.counters {
			for workerIdx := range counter.Values {
				if counter.Values[workerIdx] == nil {
					counter.Values[workerIdx] = make([]uint64, group.sizes[idx])
				}
			}
		}
		merged = append(merged, CounterGroup{
			Tags:     group.tags,
			Counters: group.counters,
		})
	}
	return merged
}

func decodeCounterTags(
	tags *C.struct_counter_tag,
	count C.size_t,
) []CounterTag {
	if tags == nil || count == 0 {
		return nil
	}

	cTags := unsafe.Slice(tags, count)
	out := make([]CounterTag, count)
	for idx := range cTags {
		out[idx] = CounterTag{
			Key:   C.GoString(cTags[idx].key),
			Value: C.GoString(cTags[idx].value),
		}
	}
	return out
}

// PerformanceCounterLatencyRange represents a latency range in performance counters.
type PerformanceCounterLatencyRange struct {
	MinLatency uint64
	Batches    uint64
}

// PerformanceCounter represents performance counter data for a module.
type PerformanceCounter struct {
	SummaryLatency uint64
	Packets        uint64
	Bytes          uint64
	MinBatchSize   uint64
	LatencyRanges  []PerformanceCounterLatencyRange
}

// PerformanceCounters represents all performance counters for a module.
type PerformanceCounters struct {
	Counters []PerformanceCounter
	Tx       uint64
	Rx       uint64
	TxBytes  uint64
	RxBytes  uint64
}

// PerformanceCounters retrieves performance counters for a specific module.
//
// Performance counters provide detailed timing and batch processing statistics
// for module execution, including mean latency and latency distribution across
// different batch sizes, as well as tx/rx packet and byte counters.
func (m *DPConfig) PerformanceCounters(
	deviceName string,
	pipelineName string,
	functionName string,
	chainName string,
	moduleType string,
	moduleName string,
) (*PerformanceCounters, error) {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	cFunctionName := C.CString(functionName)
	defer C.free(unsafe.Pointer(cFunctionName))
	cChainName := C.CString(chainName)
	defer C.free(unsafe.Pointer(cChainName))
	cModuleType := C.CString(moduleType)
	defer C.free(unsafe.Pointer(cModuleType))
	cModuleName := C.CString(moduleName)
	defer C.free(unsafe.Pointer(cModuleName))

	var cErr *C.yanet_error
	var counters C.struct_module_performance_counters
	rc := C.yanet_module_performance_counters(
		&counters,
		m.ptr,
		cDeviceName,
		cPipelineName,
		cFunctionName,
		cChainName,
		cModuleType,
		cModuleName,
		&cErr,
	)
	defer C.yanet_module_performance_counters_free(&counters)

	if rc != 0 {
		if err := cerrors.FromC(unsafe.Pointer(cErr)); err != nil {
			return nil, fmt.Errorf(
				"failed to get module performance counters: %w",
				err,
			)
		}
		return nil, fmt.Errorf("failed to get module performance counters")
	}

	perfCounters := make([]PerformanceCounter, counters.counters_count)

	// Convert C array to Go slice for iteration
	cCounters := unsafe.Slice(counters.counters, counters.counters_count)

	for idx := range perfCounters {
		cCounter := &cCounters[idx]

		latencyRanges := make(
			[]PerformanceCounterLatencyRange,
			cCounter.latency_ranges_count,
		)
		cLatencyRanges := unsafe.Slice(
			cCounter.latency_ranges,
			cCounter.latency_ranges_count,
		)

		for j := range latencyRanges {
			latencyRanges[j] = PerformanceCounterLatencyRange{
				MinLatency: uint64(cLatencyRanges[j].min_latency),
				Batches:    uint64(cLatencyRanges[j].batches),
			}
		}

		perfCounters[idx] = PerformanceCounter{
			Packets:        uint64(cCounter.packets),
			Bytes:          uint64(cCounter.bytes),
			SummaryLatency: uint64(cCounter.summary_latency),
			MinBatchSize:   uint64(cCounter.min_batch_size),
			LatencyRanges:  latencyRanges,
		}
	}

	// Access C struct fields directly
	cCountersPtr := &counters

	result := &PerformanceCounters{
		Counters: perfCounters,
		Tx:       uint64(cCountersPtr.tx),
		Rx:       uint64(cCountersPtr.rx),
		TxBytes:  uint64(cCountersPtr.tx_bytes),
		RxBytes:  uint64(cCountersPtr.rx_bytes),
	}

	return result, nil
}

// CounterSet indexes one batch of counters by name, all sharing the same instance count.
type CounterSet struct {
	counterByName map[string]CounterInfo
	instances     int
	missing       []error
}

// NewCounterSet fails when the counters carry duplicate names or disagree on their instance count.
func NewCounterSet(counters []CounterInfo) (*CounterSet, error) {
	set := &CounterSet{
		counterByName: make(map[string]CounterInfo, len(counters)),
	}
	if len(counters) == 0 {
		return set, nil
	}

	set.instances = counters[0].Instances()
	for _, counter := range counters {
		if counter.Instances() != set.instances {
			return nil, fmt.Errorf(
				"counter %q spans %d instances, expected %d",
				counter.Name,
				counter.Instances(),
				set.instances,
			)
		}
		if _, found := set.counterByName[counter.Name]; found {
			return nil, fmt.Errorf("duplicate counter %q", counter.Name)
		}
		set.counterByName[counter.Name] = counter
	}
	return set, nil
}

// Instances returns the instance count shared by every counter, or zero when the set is empty.
func (m *CounterSet) Instances() int {
	return m.instances
}

// Err reports every counter that could not be resolved, or nil when all of them resolved.
func (m *CounterSet) Err() error {
	if len(m.missing) == 0 {
		return nil
	}
	return joinedError{errs: m.missing}
}

// Lookup names a counter and its required size; zero leaves the size unchecked.
func (m *CounterSet) Lookup(name string, size int) MaybeCounter {
	return MaybeCounter{set: m, name: name, size: size}
}

// MaybeCounter is a lookup that materializes once the caller chooses how absence is reported.
type MaybeCounter struct {
	set  *CounterSet
	name string
	size int
}

// Require returns zero for an absent counter and records the miss.
func (m MaybeCounter) Require() CounterInfo {
	counter, err := m.resolve()
	if err != nil {
		m.set.missing = append(m.set.missing, err)
	}
	return counter
}

func (m MaybeCounter) resolve() (CounterInfo, error) {
	counter, found := m.set.counterByName[m.name]
	if !found {
		return m.zero(), fmt.Errorf("counter %q not found", m.name)
	}
	if m.size != 0 && counter.Size() != m.size {
		return m.zero(), fmt.Errorf(
			"counter %q has size %d, expected %d",
			m.name,
			counter.Size(),
			m.size,
		)
	}
	return counter, nil
}

func (m MaybeCounter) zero() CounterInfo {
	// A zero size intentionally yields an empty counter.
	values := make([][]uint64, m.set.instances)
	for idx := range values {
		values[idx] = make([]uint64, m.size)
	}
	return CounterInfo{Name: m.name, Values: values}
}

type joinedError struct {
	errs []error
}

func (m joinedError) Error() string {
	reasons := make([]string, 0, len(m.errs))
	for _, err := range m.errs {
		reasons = append(reasons, err.Error())
	}
	return strings.Join(reasons, "; ")
}

func (m joinedError) Unwrap() []error {
	return m.errs
}
