package fwstate_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

type mutationEvent struct{ operation, phase string }

type mutationBlock struct {
	entered chan struct{}
	release chan struct{}
}

// blockingObserver observes mutation lock events and can pause a mutation
// right after it reports acquired, holding updateMu until released.
type blockingObserver struct {
	blocks chan mutationBlock
	events chan mutationEvent
}

func newBlockingObserver() *blockingObserver {
	return &blockingObserver{blocks: make(chan mutationBlock, 4)}
}

// blockNextMutation arms the next mutation to pause once it acquires the
// mutation lock. The returned channel closes when the mutation is paused.
func (m *blockingObserver) blockNextMutation() (<-chan struct{}, func()) {
	block := mutationBlock{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	m.blocks <- block

	return block.entered, sync.OnceFunc(func() { close(block.release) })
}

func (m *blockingObserver) ObserveFWStateMutation(operation, phase string) {
	if m.events != nil {
		m.events <- mutationEvent{operation: operation, phase: phase}
	}
	if phase != "acquired" {
		return
	}
	select {
	case block := <-m.blocks:
		close(block.entered)
		<-block.release
	default:
	}
}

// fwstateStateSnapshot identifies the published state readers observe: the
// config name, the linked v4 map object's name, and the sync port.
type fwstateStateSnapshot struct {
	name      string
	mapNameV4 string
	port      uint32
}

func concurrencyUpdateRequest(
	name string,
	maps fwstateTestMaps,
	port uint32,
) *fwstatepb.UpdateConfigRequest {
	request := validDeleteTestUpdateRequest(name, maps.v4Name(), maps.v6Name())
	request.SyncConfig.PortMulticast = port
	return request
}

func publishConfig(
	t *testing.T,
	service *fwstate.FWStateService,
	request *fwstatepb.UpdateConfigRequest,
) {
	t.Helper()
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

func requirePublishedConfig(
	t *testing.T,
	service *fwstate.FWStateService,
	want fwstateStateSnapshot,
) {
	t.Helper()
	show, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: want.name})
	require.NoError(t, err)
	require.Equal(t, want.mapNameV4, show.GetMapNameV4())
	require.Equal(t, want.port, show.GetSyncConfig().GetPortMulticast())
}

func TestFWStateReadsDoNotWaitForUpdate(t *testing.T) {
	const name = "fwstate0"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-read-locks")
	tasks := newAsyncTasks(t)
	observer := newBlockingObserver()
	mapsA := newFWStateTestMaps(t, agent, "maps-a", 1024)
	mapsB := newFWStateTestMaps(t, agent, "maps-b", 2048)
	service := fwstate.NewFWStateService(agent,
		fwstate.WithMutationObserver(observer),
	)

	_, err := service.UpdateConfig(t.Context(), concurrencyUpdateRequest(name, mapsA, 9999))
	require.NoError(t, err)

	updateEntered, releaseUpdate := observer.blockNextMutation()
	t.Cleanup(releaseUpdate)
	updateDone := runAsync(tasks, func() error {
		_, err := service.UpdateConfig(t.Context(), concurrencyUpdateRequest(name, mapsB, 10000))
		return err
	})
	waitFor(t, updateEntered, "UpdateConfig did not acquire the mutation lock")
	readsDone := runAsync(tasks, func() error {
		show, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: name})
		if err != nil || show.GetMapNameV4() != mapsA.v4Name() ||
			show.GetSyncConfig().GetPortMulticast() != 9999 {
			return fmt.Errorf("ShowConfig did not return the old state: %v", err)
		}
		if _, err = service.ListConfigs(t.Context(), &fwstatepb.ListConfigsRequest{}); err != nil {
			return err
		}
		_, err = service.Metrics()
		return err
	})
	require.NoError(t, waitFor(t, readsDone, "reads waited for the in-flight update"))
	releaseUpdate()
	require.NoError(t, waitFor(t, updateDone, "UpdateConfig did not finish after release"))
	requirePublishedConfig(t, service, fwstateStateSnapshot{name: name, mapNameV4: mapsB.v4Name(), port: 10000})
}

func TestFWStateUpdateRollbackKeepsPublishedConfig(t *testing.T) {
	const name = "fwstate0"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-rollback")
	service := fwstate.NewFWStateService(agent)
	mapsA := newFWStateTestMaps(t, agent, "maps-a", 1024)
	mapsB := newFWStateTestMaps(t, agent, "maps-b", 2048)
	publishConfig(t, service, concurrencyUpdateRequest(name, mapsA, 9999))

	// A map name that resolves to no published object fails the update
	// at the generation install with InvalidArgument naming the map,
	// leaving the previous config in place.
	failing := concurrencyUpdateRequest(name, mapsB, 10000)
	failing.MapNameV4 = "no-such-map"
	_, err := service.UpdateConfig(t.Context(), failing)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, err.Error(), "no-such-map")
	requirePublishedConfig(t, service, fwstateStateSnapshot{name: name, mapNameV4: mapsA.v4Name(), port: 9999})

	publishConfig(t, service, concurrencyUpdateRequest(name, mapsB, 10000))
	requirePublishedConfig(t, service, fwstateStateSnapshot{name: name, mapNameV4: mapsB.v4Name(), port: 10000})
}

func TestFWStateMutationsRemainSerialized(t *testing.T) {
	const name = "fwstate0"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-mutations")
	tasks := newAsyncTasks(t)
	observer := newBlockingObserver()
	service := fwstate.NewFWStateService(agent, fwstate.WithMutationObserver(observer))
	mapsA := newFWStateTestMaps(t, agent, "maps-a", 1024)
	mapsB := newFWStateTestMaps(t, agent, "maps-b", 2048)
	publishConfig(t, service, concurrencyUpdateRequest(name, mapsA, 9999))
	observer.events = make(chan mutationEvent, 16)
	requireEvent := func(operation, phase string) {
		require.Equal(t, mutationEvent{operation: operation, phase: phase},
			waitFor(t, observer.events, operation+" did not reach "+phase))
	}

	updateEntered, release := observer.blockNextMutation()
	updateDone := runAsync(tasks, func() error {
		_, err := service.UpdateConfig(t.Context(), concurrencyUpdateRequest(name, mapsB, 10000))
		return err
	})
	requireEvent("update", "waiting")
	requireEvent("update", "acquired")
	waitFor(t, updateEntered, "UpdateConfig did not reach its pause point")

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
	mapsA := newFWStateTestMaps(t, agent, "maps-a", 1024)
	mapsB := newFWStateTestMaps(t, agent, "maps-b", 2048)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, concurrencyUpdateRequest(name, mapsA, 9999))
	allowedSnapshots := map[fwstateStateSnapshot]struct{}{
		{mapNameV4: mapsA.v4Name(), port: 9999}: {},
	}
	updateMaps := []fwstateTestMaps{mapsB, mapsA}
	for idx := range 6 {
		allowedSnapshots[fwstateStateSnapshot{
			mapNameV4: updateMaps[idx%2].v4Name(),
			port:      10000 + uint32(idx),
		}] = struct{}{}
	}

	start := make(chan struct{})
	var group errgroup.Group
	for range 3 {
		group.Go(func() error {
			<-start
			for range 20 {
				show, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: name})
				if err == nil {
					snapshot := fwstateStateSnapshot{
						mapNameV4: show.GetMapNameV4(),
						port:      show.GetSyncConfig().GetPortMulticast(),
					}
					if _, ok := allowedSnapshots[snapshot]; !ok {
						return fmt.Errorf("ShowConfig returned invalid snapshot %v", snapshot)
					}
				} else if status.Code(err) != codes.NotFound {
					return err
				}
				if _, err := service.Metrics(); err != nil {
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
				name, updateMaps[idx%2], 10000+uint32(idx),
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
