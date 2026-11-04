package crud_test

import (
	"sync"
	"testing"

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

// observedBackend is a fake backend that logs each shared-memory call into
// the observer's event log, so the log shows where the work runs relative
// to the reported phases.
type observedBackend struct {
	fakeBackend
	observer *recordingObserver
}

func (m *observedBackend) Update(name string, payload string) (crud.Handle, error) {
	m.observer.ObserveMutation("backend", "update")
	return m.fakeBackend.Update(name, payload)
}

func (m *observedBackend) Delete(name string) error {
	m.observer.ObserveMutation("backend", "delete")
	return m.fakeBackend.Delete(name)
}

// Test_Core_MutationObserver_OrdersPhases verifies that a publish and a
// delete each report waiting before the writer section and acquired
// before releasing, with the shared-memory work between the last two.
func Test_Core_MutationObserver_OrdersPhases(t *testing.T) {
	observer := &recordingObserver{}
	backend := &observedBackend{observer: observer}
	core := crud.NewCore(backend, crud.WithMutationObserver(observer))

	require.NoError(t, core.Replace("wan", "first"))
	require.NoError(t, core.Delete("wan"))

	require.Equal(t, [][2]string{
		{crud.OperationUpdate, crud.PhaseWaiting},
		{crud.OperationUpdate, crud.PhaseAcquired},
		{"backend", "update"},
		{crud.OperationUpdate, crud.PhaseReleasing},
		{crud.OperationDelete, crud.PhaseWaiting},
		{crud.OperationDelete, crud.PhaseAcquired},
		{"backend", "delete"},
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
