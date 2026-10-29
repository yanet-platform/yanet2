package crud_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/crud"
)

// recordingObserver records every mutation phase it observes.
type recordingObserver struct {
	mu     sync.Mutex
	events [][2]string
}

func (m *recordingObserver) ObserveMutation(operation, phase string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, [2]string{operation, phase})
}

func (m *recordingObserver) snapshot() [][2]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][2]string{}, m.events...)
}

// blockingObserver pauses the first mutation that reaches the acquired
// phase until it is released.
type blockingObserver struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingObserver() *blockingObserver {
	return &blockingObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (m *blockingObserver) ObserveMutation(operation, phase string) {
	if phase != crud.PhaseAcquired {
		return
	}
	m.once.Do(func() {
		close(m.entered)
		<-m.release
	})
}

// Test_Core_MutationObserver_OrdersPhases verifies that a publish and a
// delete each report waiting before the writer section and acquired
// before releasing, with the shared-memory work between the last two.
func Test_Core_MutationObserver_OrdersPhases(t *testing.T) {
	observer := &recordingObserver{}
	core := crud.NewCore(&fakeBackend{}, crud.WithMutationObserver(observer))

	require.NoError(t, core.Replace("wan", "first"))
	require.NoError(t, core.Delete("wan"))

	require.Equal(t, [][2]string{
		{crud.OperationUpdate, crud.PhaseWaiting},
		{crud.OperationUpdate, crud.PhaseAcquired},
		{crud.OperationUpdate, crud.PhaseReleasing},
		{crud.OperationDelete, crud.PhaseWaiting},
		{crud.OperationDelete, crud.PhaseAcquired},
		{crud.OperationDelete, crud.PhaseReleasing},
	}, observer.snapshot())
}

// Test_Core_MutationObserver_DeleteOfUnknownNameReportsWaitingOnly
// verifies that a delete the store rejects before its writer section
// reports nothing but the waiting phase.
func Test_Core_MutationObserver_DeleteOfUnknownNameReportsWaitingOnly(t *testing.T) {
	observer := &recordingObserver{}
	core := crud.NewCore(&fakeBackend{}, crud.WithMutationObserver(observer))

	require.Error(t, core.Delete("absent"))
	require.Equal(t, [][2]string{
		{crud.OperationDelete, crud.PhaseWaiting},
	}, observer.snapshot())
}

// Test_Core_MutationObserver_AcquiredHoldsWriterSection verifies that a
// mutation paused in the acquired phase holds the name's writer
// section, so a second mutation of the same name reaches neither its
// own acquired phase nor its publish until the pause ends.
func Test_Core_MutationObserver_AcquiredHoldsWriterSection(t *testing.T) {
	backend := &fakeBackend{}
	observer := newBlockingObserver()
	core := crud.NewCore(backend, crud.WithMutationObserver(observer))

	published := make(chan error, 1)
	go func() {
		published <- core.Replace("wan", "first")
	}()

	select {
	case <-observer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first update never reached its acquired phase")
	}

	second := make(chan error, 1)
	go func() {
		second <- core.Replace("wan", "second")
	}()

	// The paused update holds the writer section before its own publish,
	// so neither update may reach the backend while it does.
	require.Empty(t, backend.payloads)
	close(observer.release)

	require.NoError(t, <-published)
	require.NoError(t, <-second)
	require.Len(t, backend.payloads, 2)
}
