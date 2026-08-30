package acl_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// waiterContext signals through Parked when the waiter using it enters
// the wait phase of a shared collection.
//
// The wait phase is entered only after the waiter has joined the
// in-flight collection, so a test that releases the collection once
// Parked has fired knows this waiter was served by that collection
// rather than by a fresh one.
type waiterContext struct {
	context.Context

	parkOnce sync.Once
	parked   chan struct{}
}

func newWaiterContext(parent context.Context) *waiterContext {
	return &waiterContext{Context: parent, parked: make(chan struct{})}
}

func (m *waiterContext) Done() <-chan struct{} {
	m.parkOnce.Do(func() { close(m.parked) })
	return m.Context.Done()
}

// Parked closes once the waiter has joined the in-flight collection.
func (m *waiterContext) Parked() <-chan struct{} {
	return m.parked
}

// readBlock gates one shared-memory read: the read announces itself and
// then pauses until released.
type readBlock struct {
	entered chan struct{}
	release chan struct{}
}

// blockingCounterSpyBackend pauses one module counter read of a scrape
// and counts every read, so a test can prove that overlapping scrapes
// shared a single flight of reads.
type blockingCounterSpyBackend struct {
	*fakeBackend

	blockMu   sync.Mutex
	nextBlock *readBlock
	reads     int
}

func newBlockingCounterSpyBackend(dataplaneConfig *ffi.DPConfig) *blockingCounterSpyBackend {
	backend := newFakeBackend()
	backend.dpConfig = dataplaneConfig
	return &blockingCounterSpyBackend{fakeBackend: backend}
}

// blockNextRead arms a one-shot pause for the next module counter read,
// returning the read's entry signal and its release.
func (m *blockingCounterSpyBackend) blockNextRead() (<-chan struct{}, func()) {
	m.blockMu.Lock()
	defer m.blockMu.Unlock()

	block := &readBlock{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	m.nextBlock = block

	return block.entered, sync.OnceFunc(func() {
		close(block.release)
	})
}

// ModuleCounters pauses on the armed block, then returns a fixed
// counter set for every position.
func (m *blockingCounterSpyBackend) ModuleCounters(
	dataplaneConfig *ffi.DPConfig,
	position ffi.ModuleReference,
	counterNames []string,
) []ffi.CounterInfo {
	m.blockMu.Lock()
	m.reads++
	block := m.nextBlock
	m.nextBlock = nil
	m.blockMu.Unlock()

	if block != nil {
		close(block.entered)
		<-block.release
	}

	return []ffi.CounterInfo{
		{Name: "rx", Values: [][]uint64{{1, 100}, {2, 200}}},
		{Name: "tx", Values: [][]uint64{{3, 300}, {4, 400}}},
		{Name: "drop", Values: [][]uint64{{5, 500}, {6, 600}}},
		{Name: "pending_input", Values: [][]uint64{{7, 700}, {8, 800}}},
		{Name: "pending_output", Values: [][]uint64{{9, 900}, {10, 1000}}},
	}
}

// Reads returns the number of module counter reads observed so far.
func (m *blockingCounterSpyBackend) Reads() int {
	m.blockMu.Lock()
	defer m.blockMu.Unlock()

	return m.reads
}

// blockingRuleCounterBackend pauses rule-counter reads and feeds each
// read from a test-supplied function of the read's ordinal, so a test
// can pin sharing, freshness, and error delivery of the merged read.
type blockingRuleCounterBackend struct {
	*fakeBackend

	blockMu   sync.Mutex
	nextBlock *readBlock
	reads     int
	read      func(readIdx int) ([]ffi.CounterGroup, error)
	lastTags  []ffi.CounterTag
	lastQuery []string
}

func newBlockingRuleCounterBackend(
	dataplaneConfig *ffi.DPConfig,
	read func(readIdx int) ([]ffi.CounterGroup, error),
) *blockingRuleCounterBackend {
	backend := newFakeBackend()
	backend.dpConfig = dataplaneConfig
	return &blockingRuleCounterBackend{fakeBackend: backend, read: read}
}

// blockNextRead arms a one-shot pause for the next rule-counter read,
// returning the read's entry signal and its release.
func (m *blockingRuleCounterBackend) blockNextRead() (<-chan struct{}, func()) {
	m.blockMu.Lock()
	defer m.blockMu.Unlock()

	block := &readBlock{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	m.nextBlock = block

	return block.entered, sync.OnceFunc(func() {
		close(block.release)
	})
}

// CountersByTags pauses on the armed block, then produces the data the
// test assigned to this read's ordinal.
func (m *blockingRuleCounterBackend) CountersByTags(
	dataplaneConfig *ffi.DPConfig,
	tags []ffi.CounterTag,
	query []string,
) ([]ffi.CounterGroup, error) {
	m.blockMu.Lock()
	readIdx := m.reads
	m.reads++
	m.lastTags = append([]ffi.CounterTag(nil), tags...)
	m.lastQuery = append([]string(nil), query...)
	block := m.nextBlock
	m.nextBlock = nil
	m.blockMu.Unlock()

	if block != nil {
		close(block.entered)
		<-block.release
	}

	return m.read(readIdx)
}

// Reads returns the number of rule-counter reads observed so far.
func (m *blockingRuleCounterBackend) Reads() int {
	m.blockMu.Lock()
	defer m.blockMu.Unlock()

	return m.reads
}

// LastRead returns the tags and the name query of the most recent
// rule-counter read.
func (m *blockingRuleCounterBackend) LastRead() ([]ffi.CounterTag, []string) {
	m.blockMu.Lock()
	defer m.blockMu.Unlock()

	return m.lastTags, m.lastQuery
}

// ruleCounterGroups builds two tagged rule-counter groups, one per
// config name, whose counters carry the given nonzero packet and byte
// totals so zero-suppression keeps every counter.
func ruleCounterGroups(packets uint64) []ffi.CounterGroup {
	return []ffi.CounterGroup{
		{
			Tags: []ffi.CounterTag{
				{Key: "module_name", Value: "acl0"},
				{Key: "device", Value: "port0"},
				{Key: "pipeline", Value: "pipeline0"},
				{Key: "function", Value: "function0"},
				{Key: "chain", Value: "chain0"},
			},
			Counters: []ffi.CounterInfo{
				{Name: "rule_a", Values: [][]uint64{{packets, packets * 10}}},
			},
		},
		{
			Tags: []ffi.CounterTag{
				{Key: "module_name", Value: "acl1"},
				{Key: "device", Value: "port1"},
				{Key: "pipeline", Value: "pipeline1"},
				{Key: "function", Value: "function1"},
				{Key: "chain", Value: "chain1"},
			},
			Counters: []ffi.CounterInfo{
				{Name: "rule_b", Values: [][]uint64{{packets, packets * 10}}},
			},
		},
	}
}

// Test_ACLMetrics_ConcurrentScrapesShareOneCounterRead verifies that two
// scrapes overlapping on the structural counter family share one flight
// of position reads and receive equal metric sets.
func Test_ACLMetrics_ConcurrentScrapesShareOneCounterRead(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingCounterSpyBackend(agent.DPConfig())
	service := acl.NewACLService(backend)

	entered, release := backend.blockNextRead()
	t.Cleanup(release)

	var group errgroup.Group
	var first, second []*commonpb.Metric
	group.Go(func() error {
		collected, err := service.Metrics(t.Context())
		first = collected
		return err
	})

	select {
	case <-entered:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("first scrape did not reach the blocked counter read")
	}

	waiterCtx := newWaiterContext(t.Context())
	group.Go(func() error {
		collected, err := service.Metrics(waiterCtx)
		second = collected
		return err
	})

	select {
	case <-waiterCtx.Parked():
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("second scrape did not join the in-flight collection")
	}

	release()
	require.NoError(t, group.Wait())

	require.Equal(t, first, second)
	require.Equal(t, 5, backend.Reads())
}

// Test_ACLMetrics_ConcurrentRuleScrapesShareOneCounterRead verifies that
// two rule scrapes with different selectors share one read and each
// receives its own selection of the same values.
func Test_ACLMetrics_ConcurrentRuleScrapesShareOneCounterRead(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingRuleCounterBackend(agent.DPConfig(), func(readIdx int) ([]ffi.CounterGroup, error) {
		return ruleCounterGroups(7), nil
	})
	service := acl.NewACLService(backend)

	entered, release := backend.blockNextRead()
	t.Cleanup(release)

	var group errgroup.Group
	var all, selected []*commonpb.Metric
	group.Go(func() error {
		collected, err := service.RuleMetrics(t.Context(), &aclpb.GetMetricsRulesRequest{})
		all = collected
		return err
	})

	select {
	case <-entered:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("first rule scrape did not reach the blocked counter read")
	}

	waiterCtx := newWaiterContext(t.Context())
	group.Go(func() error {
		collected, err := service.RuleMetrics(waiterCtx, &aclpb.GetMetricsRulesRequest{Config: "acl0"})
		selected = collected
		return err
	})

	select {
	case <-waiterCtx.Parked():
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("second rule scrape did not join the in-flight collection")
	}

	release()
	require.NoError(t, group.Wait())

	require.Equal(t, 1, backend.Reads())
	tags, query := backend.LastRead()
	require.Equal(t, []ffi.CounterTag{
		{Key: "module_type", Value: "acl"},
		{Key: "kind", Value: "runtime"},
	}, tags)
	require.Empty(t, query)

	fullPackets := findMetricWithLabels(
		all,
		"acl_rule_packets",
		map[string]string{"config": "acl0", "counter": "rule_a"},
	)
	require.NotNil(t, fullPackets, "unrestricted scrape must carry both configs")
	selectedPackets := findMetricWithLabels(
		selected,
		"acl_rule_packets",
		map[string]string{"config": "acl0", "counter": "rule_a"},
	)
	require.NotNil(t, selectedPackets)
	require.Equal(t, fullPackets.GetCounter(), selectedPackets.GetCounter())
	require.Nil(t, findMetricWithLabels(
		selected,
		"acl_rule_packets",
		map[string]string{"config": "acl1", "counter": "rule_b"},
	), "selected scrape must drop the unselected config")
}

// Test_ACLMetrics_RequestAfterCompletedFlightStartsNewRead verifies that
// a rule scrape arriving after a completed flight starts a fresh read
// instead of reusing the earlier result.
func Test_ACLMetrics_RequestAfterCompletedFlightStartsNewRead(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingRuleCounterBackend(agent.DPConfig(), func(readIdx int) ([]ffi.CounterGroup, error) {
		return ruleCounterGroups(uint64(10 * (readIdx + 1))), nil
	})
	service := acl.NewACLService(backend)

	first, err := service.RuleMetrics(t.Context(), &aclpb.GetMetricsRulesRequest{})
	require.NoError(t, err)
	second, err := service.RuleMetrics(t.Context(), &aclpb.GetMetricsRulesRequest{})
	require.NoError(t, err)

	require.Equal(t, 2, backend.Reads())
	firstPackets := findMetricWithLabels(
		first,
		"acl_rule_packets",
		map[string]string{"config": "acl0", "counter": "rule_a"},
	)
	require.NotNil(t, firstPackets)
	require.Equal(t, uint64(10), firstPackets.GetCounter())
	secondPackets := findMetricWithLabels(
		second,
		"acl_rule_packets",
		map[string]string{"config": "acl0", "counter": "rule_a"},
	)
	require.NotNil(t, secondPackets)
	require.Equal(t, uint64(20), secondPackets.GetCounter())
}

// Test_ACLMetrics_ErrorReachesAllWaiters verifies that a failed merged
// read reaches every overlapping scraper as the same internal error.
func Test_ACLMetrics_ErrorReachesAllWaiters(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingRuleCounterBackend(agent.DPConfig(), func(readIdx int) ([]ffi.CounterGroup, error) {
		return nil, errors.New("counter read failed")
	})
	service := acl.NewACLService(backend)

	entered, release := backend.blockNextRead()
	t.Cleanup(release)

	scraper := func(ctx context.Context) func() error {
		return func() error {
			_, err := service.RuleMetrics(ctx, &aclpb.GetMetricsRulesRequest{})
			return err
		}
	}

	var group errgroup.Group
	failures := make(chan error, 2)
	group.Go(func() error {
		failures <- scraper(t.Context())()
		return nil
	})

	select {
	case <-entered:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("first rule scrape did not reach the blocked counter read")
	}

	waiterCtx := newWaiterContext(t.Context())
	group.Go(func() error {
		failures <- scraper(waiterCtx)()
		return nil
	})

	select {
	case <-waiterCtx.Parked():
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("second rule scrape did not join the in-flight collection")
	}

	release()
	require.NoError(t, group.Wait())
	close(failures)

	require.Equal(t, 1, backend.Reads())
	var firstErr, secondErr error
	for failure := range failures {
		if firstErr == nil {
			firstErr = failure
		} else {
			secondErr = failure
		}
	}
	require.Error(t, firstErr)
	require.Error(t, secondErr)
	require.Equal(t, codes.Internal, status.Code(firstErr))
	require.Equal(t, codes.Internal, status.Code(secondErr))
	require.Equal(t, firstErr.Error(), secondErr.Error())
}

// Test_ACLService_DrainMetricsReads_WaitsForInFlightCollection verifies
// that draining blocks until a collection abandoned by a cancelled
// waiter finishes, so the release waiting behind it cannot unmap memory
// under the read.
func Test_ACLService_DrainMetricsReads_WaitsForInFlightCollection(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingRuleCounterBackend(agent.DPConfig(), func(readIdx int) ([]ffi.CounterGroup, error) {
		return ruleCounterGroups(7), nil
	})
	service := acl.NewACLService(backend)

	entered, release := backend.blockNextRead()
	t.Cleanup(release)

	type ruleResult struct {
		metrics []*commonpb.Metric
		err     error
	}
	leaderDone := make(chan ruleResult, 1)
	go func() {
		collected, err := service.RuleMetrics(t.Context(), &aclpb.GetMetricsRulesRequest{})
		leaderDone <- ruleResult{metrics: collected, err: err}
	}()

	select {
	case <-entered:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("leader scrape did not reach the blocked counter read")
	}

	waiterParent, cancelWaiter := context.WithCancel(t.Context())
	t.Cleanup(cancelWaiter)
	waiterCtx := newWaiterContext(waiterParent)
	waiterDone := make(chan error, 1)
	go func() {
		_, err := service.RuleMetrics(waiterCtx, &aclpb.GetMetricsRulesRequest{})
		waiterDone <- err
	}()

	select {
	case <-waiterCtx.Parked():
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("abandoned scraper did not join the in-flight collection")
	}

	cancelWaiter()
	select {
	case err := <-waiterDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("abandoned scraper did not return")
	}

	drainDone := make(chan struct{})
	go func() {
		service.DrainMetricsReads()
		close(drainDone)
	}()

	select {
	case <-drainDone:
		t.Fatal("drain returned while the collection was still blocked")
	case <-time.After(50 * time.Millisecond):
	}

	release()

	select {
	case <-drainDone:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("drain did not return after the collection finished")
	}

	var leader ruleResult
	select {
	case leader = <-leaderDone:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("leader scrape did not finish after the release")
	}
	require.NoError(t, leader.err)
	require.Len(t, leader.metrics, 4)
}

// Test_ACLMetrics_CancelledWaiterDoesNotAbortFlight verifies that
// cancelling one waiter neither aborts the shared read nor leaves its
// result behind for the next scraper.
func Test_ACLMetrics_CancelledWaiterDoesNotAbortFlight(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingRuleCounterBackend(agent.DPConfig(), func(readIdx int) ([]ffi.CounterGroup, error) {
		return ruleCounterGroups(7), nil
	})
	service := acl.NewACLService(backend)

	entered, release := backend.blockNextRead()
	t.Cleanup(release)

	type ruleResult struct {
		metrics []*commonpb.Metric
		err     error
	}
	leaderDone := make(chan ruleResult, 1)
	go func() {
		collected, err := service.RuleMetrics(t.Context(), &aclpb.GetMetricsRulesRequest{})
		leaderDone <- ruleResult{metrics: collected, err: err}
	}()

	select {
	case <-entered:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("leader scrape did not reach the blocked counter read")
	}

	waiterParent, cancelWaiter := context.WithCancel(t.Context())
	t.Cleanup(cancelWaiter)
	waiterCtx := newWaiterContext(waiterParent)
	waiterDone := make(chan error, 1)
	go func() {
		_, err := service.RuleMetrics(waiterCtx, &aclpb.GetMetricsRulesRequest{})
		waiterDone <- err
	}()

	select {
	case <-waiterCtx.Parked():
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("cancellable scraper did not join the in-flight collection")
	}

	cancelWaiter()
	select {
	case err := <-waiterDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("cancelled scraper did not return")
	}

	release()

	var leader ruleResult
	select {
	case leader = <-leaderDone:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("leader scrape did not finish after the release")
	}
	require.NoError(t, leader.err)
	require.Len(t, leader.metrics, 4)
	require.NotNil(t, findMetricWithLabels(
		leader.metrics,
		"acl_rule_packets",
		map[string]string{"config": "acl0", "counter": "rule_a"},
	), "leader scrape must receive the full result")

	third, err := service.RuleMetrics(t.Context(), &aclpb.GetMetricsRulesRequest{})
	require.NoError(t, err)
	require.Equal(t, 2, backend.Reads())
	require.NotNil(t, findMetricWithLabels(
		third,
		"acl_rule_packets",
		map[string]string{"config": "acl0", "counter": "rule_a"},
	), "post-completion scrape must run a fresh read")
}

// Test_ACLMetrics_RulesCountersShareRuleScrapeRead verifies that a
// rules-counters request overlapping a rule scrape joins its in-flight
// read instead of starting a second one, and filters the shared result
// on its own.
func Test_ACLMetrics_RulesCountersShareRuleScrapeRead(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingRuleCounterBackend(agent.DPConfig(), func(readIdx int) ([]ffi.CounterGroup, error) {
		return ruleCounterGroups(7), nil
	})
	service := acl.NewACLService(backend)

	entered, release := backend.blockNextRead()
	t.Cleanup(release)

	var group errgroup.Group
	rulesDone := make(chan []*commonpb.Metric, 1)
	group.Go(func() error {
		collected, err := service.RuleMetrics(t.Context(), &aclpb.GetMetricsRulesRequest{})
		rulesDone <- collected
		return err
	})

	select {
	case <-entered:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("rule scrape did not reach the blocked counter read")
	}

	waiterCtx := newWaiterContext(t.Context())
	countersDone := make(chan []*aclpb.RuleCounter, 1)
	group.Go(func() error {
		response, err := service.GetRulesCounters(waiterCtx, &aclpb.GetRulesCountersRequest{})
		if err != nil {
			return err
		}
		countersDone <- response.GetCounters()
		return nil
	})

	select {
	case <-waiterCtx.Parked():
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("rules-counters request did not join the in-flight read")
	}

	release()
	require.NoError(t, group.Wait())

	require.Equal(t, 1, backend.Reads())
	tags, query := backend.LastRead()
	require.Equal(t, []ffi.CounterTag{
		{Key: "module_type", Value: "acl"},
		{Key: "kind", Value: "runtime"},
	}, tags)
	require.Empty(t, query)

	rules := <-rulesDone
	require.NotNil(t, findMetricWithLabels(
		rules,
		"acl_rule_packets",
		map[string]string{"config": "acl0", "counter": "rule_a"},
	), "rule scrape must receive the full result")

	counters := <-countersDone
	require.Len(t, counters, 2)
	first := counters[0]
	require.Equal(t, "acl0", first.GetConfig())
	require.Equal(t, "port0", first.GetDevice())
	require.Equal(t, "pipeline0", first.GetPipeline())
	require.Equal(t, "function0", first.GetFunction())
	require.Equal(t, "chain0", first.GetChain())
	require.Equal(t, "rule_a", first.GetCounter())
	require.Equal(t, uint64(7), first.GetPackets())
	require.Equal(t, uint64(70), first.GetBytes())
}

// Test_ACLMetrics_RuleMetricsRejectsUncarriableSelector verifies that a
// selector value the counter-tag fields cannot carry is rejected as an
// invalid argument before any shared-memory read.
func Test_ACLMetrics_RuleMetricsRejectsUncarriableSelector(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingRuleCounterBackend(agent.DPConfig(), func(readIdx int) ([]ffi.CounterGroup, error) {
		return ruleCounterGroups(7), nil
	})
	service := acl.NewACLService(backend)

	requests := map[string]*aclpb.GetMetricsRulesRequest{
		"nul byte in config": {Config: "acl\x00"},
		"overlong device":    {Device: strings.Repeat("a", 80)},
	}
	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			_, err := service.RuleMetrics(t.Context(), request)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Contains(t, err.Error(), "invalid counter tag")
		})
	}

	require.Equal(t, 0, backend.Reads(), "rejected selectors must not reach the shared read")
}

// Test_ACLService_DrainMetricsReads_WaitsForRulesCounters verifies that
// the drain also blocks behind a rules-counters request's shared read,
// so the release waiting behind it cannot unmap memory under the read.
func Test_ACLService_DrainMetricsReads_WaitsForRulesCounters(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	backend := newBlockingRuleCounterBackend(agent.DPConfig(), func(readIdx int) ([]ffi.CounterGroup, error) {
		return ruleCounterGroups(7), nil
	})
	service := acl.NewACLService(backend)

	entered, release := backend.blockNextRead()
	t.Cleanup(release)

	type countersResult struct {
		counters []*aclpb.RuleCounter
		err      error
	}
	leaderDone := make(chan countersResult, 1)
	go func() {
		response, err := service.GetRulesCounters(t.Context(), &aclpb.GetRulesCountersRequest{})
		var counters []*aclpb.RuleCounter
		if err == nil {
			counters = response.GetCounters()
		}
		leaderDone <- countersResult{counters: counters, err: err}
	}()

	select {
	case <-entered:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("rules-counters request did not reach the blocked counter read")
	}

	drainDone := make(chan struct{})
	go func() {
		service.DrainMetricsReads()
		close(drainDone)
	}()

	select {
	case <-drainDone:
		t.Fatal("drain returned while the rules-counters read was still blocked")
	case <-time.After(50 * time.Millisecond):
	}

	release()

	select {
	case <-drainDone:
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("drain did not return after the rules-counters read finished")
	}

	select {
	case leader := <-leaderDone:
		require.NoError(t, leader.err)
		require.Len(t, leader.counters, 2)
	case <-time.After(metricsConcurrencyTestTimeout):
		t.Fatal("rules-counters request did not finish after the release")
	}
}
