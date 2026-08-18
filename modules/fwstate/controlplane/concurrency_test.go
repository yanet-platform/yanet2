package fwstate_test

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	fwstatepb "github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

const fwstateConcurrencyTimeout = 5 * time.Second

func newAsyncTasks(t *testing.T) *errgroup.Group {
	t.Helper()
	tasks := &errgroup.Group{}
	t.Cleanup(func() { require.NoError(t, tasks.Wait()) })
	return tasks
}

func runAsync[T any](tasks *errgroup.Group, run func() T) <-chan T {
	result := make(chan T, 1)
	tasks.Go(func() error {
		result <- run()
		return nil
	})
	return result
}

type providerAction struct {
	entered      chan struct{}
	release      <-chan struct{}
	linkedConfig []ffi.ModuleConfig
	insertLayer  *fwstatepb.MapConfig
	err          error
}
type mutationEvent struct{ operation, phase string }
type blockingACLProvider struct {
	relinks      chan providerAction
	links        chan providerAction
	observations chan mutationEvent
}

func newBlockingACLProvider() *blockingACLProvider {
	return &blockingACLProvider{relinks: make(chan providerAction, 16), links: make(chan providerAction, 4)}
}

func (m *blockingACLProvider) queueRelink(release <-chan struct{}, err error) <-chan struct{} {
	entered := make(chan struct{})
	m.relinks <- providerAction{entered: entered, release: release, err: err}
	return entered
}

func (m *blockingACLProvider) LinkedConfigNames(string) []string { return nil }

func (m *blockingACLProvider) ObserveFWStateMutation(operation, phase string) {
	if m.observations != nil {
		m.observations <- mutationEvent{operation: operation, phase: phase}
	}
}

func (m *blockingACLProvider) RelinkConfigs(
	config *fwstate.FwStateConfig,
	publish func([]ffi.ModuleConfig) error,
) error {
	action := <-m.relinks
	close(action.entered)
	if action.release != nil {
		<-action.release
	}
	if action.err == nil && action.insertLayer != nil {
		action.err = config.InsertLayer(action.insertLayer, 1)
	}
	if action.err != nil {
		return action.err
	}
	return publish(action.linkedConfig)
}

func (m *blockingACLProvider) LinkConfigs(
	_ []string,
	_ *fwstate.FwStateConfig,
	publish func([]ffi.ModuleConfig) error,
) error {
	action := <-m.links
	close(action.entered)
	return publish(nil)
}

type entriesTestStream struct {
	grpc.ServerStream
	request     *fwstatepb.ListEntriesRequest
	received    bool
	sendEntered chan struct{}
	releaseSend <-chan struct{}
}

func (m *entriesTestStream) Recv() (*fwstatepb.ListEntriesRequest, error) {
	if m.received {
		return nil, io.EOF
	}
	m.received = true
	return m.request, nil
}

func (m *entriesTestStream) Send(*fwstatepb.ListEntriesResponse) error {
	if m.sendEntered != nil {
		close(m.sendEntered)
	}
	if m.releaseSend != nil {
		<-m.releaseSend
	}
	return nil
}

func concurrencyUpdateRequest(name string, indexSize, port uint32) *fwstatepb.UpdateConfigRequest {
	request := validDeleteTestUpdateRequest(name)
	request.MapConfig.IndexSize = indexSize
	request.SyncConfig.PortMulticast = port
	return request
}

func publishConfig(
	t *testing.T,
	service *fwstate.FWStateService,
	provider *blockingACLProvider,
	request *fwstatepb.UpdateConfigRequest,
) {
	t.Helper()
	provider.queueRelink(nil, nil)
	_, err := service.UpdateConfig(t.Context(), request)
	require.NoError(t, err)
}

func waitFor[T any](t *testing.T, result <-chan T, message string) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(fwstateConcurrencyTimeout):
		t.Fatal(message)
	}
	panic("unreachable")
}

func releaseOnCleanup(t *testing.T, channel chan struct{}) func() {
	release := sync.OnceFunc(func() { close(channel) })
	t.Cleanup(release)
	return release
}

func requirePublishedConfig(
	t *testing.T,
	service *fwstate.FWStateService,
	name string,
	indexSize, port uint32,
) {
	t.Helper()
	show, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: name})
	require.NoError(t, err)
	require.Equal(t, indexSize, show.GetMapConfig().GetIndexSize())
	require.Equal(t, port, show.GetSyncConfig().GetPortMulticast())
}

func TestFWStateReadsDoNotWaitForRelink(t *testing.T) {
	const name = "fwstate0"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-read-locks")
	tasks := newAsyncTasks(t)
	provider := newBlockingACLProvider()
	service := fwstate.NewFWStateService(agent, provider)
	provider.relinks <- providerAction{
		entered:     make(chan struct{}),
		insertLayer: &fwstatepb.MapConfig{IndexSize: 2048, ExtraBucketCount: 64},
	}
	_, err := service.UpdateConfig(t.Context(), concurrencyUpdateRequest(name, 1024, 9999))
	require.NoError(t, err)
	releaseChannel := make(chan struct{})
	release := releaseOnCleanup(t, releaseChannel)
	updateEntered := provider.queueRelink(releaseChannel, nil)
	updateDone := runAsync(tasks, func() error {
		_, err := service.UpdateConfig(t.Context(), concurrencyUpdateRequest(name, 2048, 10000))
		return err
	})
	waitFor(t, updateEntered, "UpdateConfig did not reach RelinkConfigs")
	readsDone := runAsync(tasks, func() error {
		show, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: name})
		if err != nil || show.GetMapConfig().GetIndexSize() != 2048 ||
			show.GetSyncConfig().GetPortMulticast() != 9999 {
			return fmt.Errorf("ShowConfig did not return the old state: %v", err)
		}
		if _, err = service.ListConfigs(t.Context(), &fwstatepb.ListConfigsRequest{}); err != nil {
			return err
		}
		stats, err := service.GetStats(t.Context(), &fwstatepb.GetStatsRequest{Name: name})
		if err != nil || stats.GetIpv4Stats().GetLayerCount() != 2 {
			return fmt.Errorf("GetStats did not return the old state: %v", err)
		}
		metrics, err := service.Metrics()
		if err != nil {
			return err
		}
		for _, metric := range metrics {
			if metric.GetName() == "fwstate_layer_count" && metric.GetGauge() == 2 {
				stream := &entriesTestStream{request: &fwstatepb.ListEntriesRequest{ConfigName: name, LayerIndex: 1, BatchSize: 1}}
				return service.ListEntries(stream)
			}
		}
		return errors.New("Metrics did not return the old state")
	})
	require.NoError(t, waitFor(t, readsDone, "reads waited for RelinkConfigs"))
	release()
	require.NoError(t, waitFor(t, updateDone, "UpdateConfig did not finish after release"))
	requirePublishedConfig(t, service, name, 2048, 10000)
	require.Error(t, service.ListEntries(&entriesTestStream{request: &fwstatepb.ListEntriesRequest{ConfigName: name, LayerIndex: 1, BatchSize: 1}}))
}

func TestFWStateUpdateRollbackKeepsPublishedConfig(t *testing.T) {
	const name = "fwstate0"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-rollback")
	provider := newBlockingACLProvider()
	service := fwstate.NewFWStateService(agent, provider)
	publishConfig(t, service, provider, concurrencyUpdateRequest(name, 1024, 9999))

	provider.queueRelink(nil, errors.New("relink failed"))
	_, err := service.UpdateConfig(t.Context(), concurrencyUpdateRequest(name, 2048, 10000))
	require.Equal(t, codes.Internal, status.Code(err))
	requirePublishedConfig(t, service, name, 1024, 9999)

	provider.relinks <- providerAction{entered: make(chan struct{}), linkedConfig: []ffi.ModuleConfig{{}}}
	_, err = service.UpdateConfig(t.Context(), concurrencyUpdateRequest(name, 2048, 10001))
	require.Equal(t, codes.Internal, status.Code(err))
	requirePublishedConfig(t, service, name, 1024, 9999)

	publishConfig(t, service, provider, concurrencyUpdateRequest(name, 2048, 10000))
	requirePublishedConfig(t, service, name, 2048, 10000)
}

func TestFWStateListEntriesUnlocksBeforeSend(t *testing.T) {
	const name = "fwstate0"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-entry-send")
	tasks := newAsyncTasks(t)
	provider := newBlockingACLProvider()
	service := fwstate.NewFWStateService(agent, provider)
	publishConfig(t, service, provider, concurrencyUpdateRequest(name, 1024, 9999))

	releaseChannel := make(chan struct{})
	release := releaseOnCleanup(t, releaseChannel)
	sendEntered := make(chan struct{})
	stream := &entriesTestStream{
		request:     &fwstatepb.ListEntriesRequest{ConfigName: name, BatchSize: 1},
		sendEntered: sendEntered,
		releaseSend: releaseChannel,
	}
	streamDone := runAsync(tasks, func() error { return service.ListEntries(stream) })
	waitFor(t, sendEntered, "ListEntries did not reach stream.Send")
	deleteDone := runAsync(tasks, func() error {
		_, err := service.DeleteConfig(t.Context(), &fwstatepb.DeleteConfigRequest{Name: name})
		return err
	})
	require.NoError(t, waitFor(t, deleteDone, "DeleteConfig waited for stream.Send"))
	release()
	require.NoError(t, waitFor(t, streamDone, "ListEntries did not finish after release"))
}

func TestFWStateMutationsRemainSerialized(t *testing.T) {
	const name = "fwstate0"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-mutations")
	tasks := newAsyncTasks(t)
	provider := newBlockingACLProvider()
	service := fwstate.NewFWStateService(agent, provider)
	publishConfig(t, service, provider, concurrencyUpdateRequest(name, 1024, 9999))
	provider.observations = make(chan mutationEvent, 16)
	requireEvent := func(operation, phase string) {
		require.Equal(t, mutationEvent{operation: operation, phase: phase},
			waitFor(t, provider.observations, operation+" did not reach "+phase))
	}

	blockUpdate := func(indexSize, port uint32) (func(), <-chan error) {
		releaseChannel := make(chan struct{})
		release := releaseOnCleanup(t, releaseChannel)
		entered := provider.queueRelink(releaseChannel, nil)
		done := runAsync(tasks, func() error {
			_, err := service.UpdateConfig(t.Context(), concurrencyUpdateRequest(name, indexSize, port))
			return err
		})
		requireEvent("update", "waiting")
		requireEvent("update", "acquired")
		waitFor(t, entered, "UpdateConfig did not reach RelinkConfigs")
		return release, done
	}

	release, updateDone := blockUpdate(2048, 10000)
	linkEntered := make(chan struct{})
	provider.links <- providerAction{entered: linkEntered}
	linkDone := runAsync(tasks, func() error {
		_, err := service.LinkFWState(t.Context(), &fwstatepb.LinkFWStateRequest{
			FwstateName: name, AclConfigNames: []string{"acl0"},
		})
		return err
	})
	requireEvent("link", "waiting")
	release()
	requireEvent("update", "releasing")
	requireEvent("link", "acquired")
	require.NoError(t, waitFor(t, updateDone, "UpdateConfig did not finish"))
	waitFor(t, linkEntered, "LinkFWState did not run after UpdateConfig")
	require.NoError(t, waitFor(t, linkDone, "LinkFWState did not finish"))
	requireEvent("link", "releasing")

	release, updateDone = blockUpdate(1024, 10001)
	deleteDone := runAsync(tasks, func() error {
		_, err := service.DeleteConfig(t.Context(), &fwstatepb.DeleteConfigRequest{Name: name})
		return err
	})
	requireEvent("delete", "waiting")
	release()
	requireEvent("update", "releasing")
	requireEvent("delete", "acquired")
	require.NoError(t, waitFor(t, updateDone, "UpdateConfig did not finish"))
	require.NoError(t, waitFor(t, deleteDone, "DeleteConfig did not finish"))
	requireEvent("delete", "releasing")
}

func TestFWStateConcurrentReadersUpdateAndDelete(t *testing.T) {
	const name = "fwstate0"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-lifetime")
	provider := newBlockingACLProvider()
	service := fwstate.NewFWStateService(agent, provider)
	publishConfig(t, service, provider, concurrencyUpdateRequest(name, 1024, 9999))
	allowedSnapshots := map[[2]uint32]struct{}{{1024, 9999}: {}}
	for idx := range 6 {
		provider.queueRelink(nil, nil)
		allowedSnapshots[[2]uint32{1024 + uint32(idx%2)*1024, 10000 + uint32(idx)}] = struct{}{}
	}

	start := make(chan struct{})
	var group errgroup.Group
	for range 3 {
		group.Go(func() error {
			<-start
			for range 20 {
				show, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: name})
				if err == nil {
					snapshot := [2]uint32{show.GetMapConfig().GetIndexSize(), show.GetSyncConfig().GetPortMulticast()}
					if _, ok := allowedSnapshots[snapshot]; !ok {
						return fmt.Errorf("ShowConfig returned invalid snapshot %v", snapshot)
					}
				} else if status.Code(err) != codes.NotFound {
					return err
				}
				stats, err := service.GetStats(t.Context(), &fwstatepb.GetStatsRequest{Name: name})
				if err == nil {
					indexSize := stats.GetIpv4Stats().GetIndexSize()
					if indexSize != 1024 && indexSize != 2048 || stats.GetIpv6Stats().GetIndexSize() != indexSize {
						return fmt.Errorf("GetStats returned invalid index sizes: %d/%d", indexSize, stats.GetIpv6Stats().GetIndexSize())
					}
				} else if status.Code(err) != codes.NotFound {
					return err
				}
				metrics, err := service.Metrics()
				if err != nil {
					return err
				}
				for _, metric := range metrics {
					if metric.GetName() == "fwstate_index_size" && metric.GetGauge() != 1024 && metric.GetGauge() != 2048 {
						return fmt.Errorf("Metrics returned invalid index size: %v", metric.GetGauge())
					}
				}
				stream := &entriesTestStream{request: &fwstatepb.ListEntriesRequest{ConfigName: name, BatchSize: 1}}
				if err := service.ListEntries(stream); err != nil && status.Code(err) != codes.NotFound {
					return err
				}
			}
			return nil
		})
	}
	group.Go(func() error {
		<-start
		for idx := range 6 {
			if _, err := service.UpdateConfig(t.Context(), concurrencyUpdateRequest(
				name, 1024+uint32(idx%2)*1024, 10000+uint32(idx),
			)); err != nil {
				return err
			}
		}
		_, err := service.DeleteConfig(t.Context(), &fwstatepb.DeleteConfigRequest{Name: name})
		return err
	})
	close(start)
	require.NoError(t, group.Wait())
	_, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: name})
	require.Equal(t, codes.NotFound, status.Code(err))
}
