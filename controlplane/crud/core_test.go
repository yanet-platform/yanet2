package crud_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// stubHandle counts its frees and can report a live generation the
// first time it is freed.
type stubHandle struct {
	frees      int
	refuseOnce bool
}

func (m *stubHandle) Free() error {
	m.frees++
	if m.refuseOnce && m.frees == 1 {
		return ffi.ErrStillReferenced
	}
	return nil
}

// fakeBackend records every payload it is asked to publish and hands
// out one fresh handle per successful update.
type fakeBackend struct {
	payloads  []string
	deleted   []string
	handles   []*stubHandle
	updateErr error
	deleteErr error
}

func (m *fakeBackend) Update(name string, payload string) (crud.Handle, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}

	m.payloads = append(m.payloads, payload)
	handle := &stubHandle{}
	m.handles = append(m.handles, handle)
	return handle, nil
}

func (m *fakeBackend) Delete(name string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}

	m.deleted = append(m.deleted, name)
	return nil
}

// Test_Core_Replace_PublishesAndRetires verifies that a replace
// publishes the new payload and frees the handle of the generation it
// retired.
func Test_Core_Replace_PublishesAndRetires(t *testing.T) {
	backend := &fakeBackend{}
	core := crud.NewCore(backend)

	require.NoError(t, core.Replace("wan", "first"))
	require.NoError(t, core.Replace("wan", "second"))

	payload, ok := core.Get("wan")
	require.True(t, ok)
	require.Equal(t, "second", payload)
	require.Equal(t, []string{"first", "second"}, backend.payloads)
	require.Equal(t, 1, backend.handles[0].frees)
	require.Equal(t, 0, backend.handles[1].frees)
}

// Test_Core_Replace_BackendFailureKeepsPrevious verifies that a failed
// replace leaves the previous payload published and its handle owned.
func Test_Core_Replace_BackendFailureKeepsPrevious(t *testing.T) {
	backend := &fakeBackend{}
	core := crud.NewCore(backend)

	require.NoError(t, core.Replace("wan", "first"))
	backend.updateErr = errors.New("shared memory refused the config")

	require.Error(t, core.Replace("wan", "second"))
	payload, ok := core.Get("wan")
	require.True(t, ok)
	require.Equal(t, "first", payload)
	require.Equal(t, 0, backend.handles[0].frees)
}

// Test_Core_Mutate_SeedsCallbackWithCurrent verifies that a mutation
// sees the published payload, or the zero payload on a new name, and
// publishes what it returns.
func Test_Core_Mutate_SeedsCallbackWithCurrent(t *testing.T) {
	backend := &fakeBackend{}
	core := crud.NewCore(backend)

	require.NoError(t, core.Mutate("wan", func(current string, ok bool) (string, error) {
		require.False(t, ok)
		require.Empty(t, current)
		return "first", nil
	}))
	require.NoError(t, core.Mutate("wan", func(current string, ok bool) (string, error) {
		require.True(t, ok)
		require.Equal(t, "first", current)
		return "second", nil
	}))

	payload, ok := core.Get("wan")
	require.True(t, ok)
	require.Equal(t, "second", payload)
}

// Test_Core_Mutate_CallbackErrorSkipsBackend verifies that a mutation
// the callback rejects reaches neither the backend nor the store.
func Test_Core_Mutate_CallbackErrorSkipsBackend(t *testing.T) {
	backend := &fakeBackend{}
	core := crud.NewCore(backend)

	require.NoError(t, core.Replace("wan", "first"))
	refused := errors.New("invalid payload")
	require.ErrorIs(t, core.Mutate("wan", func(string, bool) (string, error) {
		return "", refused
	}), refused)

	require.Equal(t, []string{"first"}, backend.payloads)
	payload, ok := core.Get("wan")
	require.True(t, ok)
	require.Equal(t, "first", payload)
}

// Test_Core_Delete_RefusalKeepsConfigPublished verifies that a delete
// the backend refuses leaves the config published under its name.
func Test_Core_Delete_RefusalKeepsConfigPublished(t *testing.T) {
	backend := &fakeBackend{deleteErr: errors.New("the dataplane refused the drop")}
	core := crud.NewCore(backend)

	require.NoError(t, core.Replace("wan", "first"))
	require.Error(t, core.Delete("wan"))

	_, ok := core.Get("wan")
	require.True(t, ok)
	require.Equal(t, []string{"wan"}, core.List())
	require.Empty(t, backend.deleted)
}

// Test_Core_Replace_FreesParkedHandleOnNextMutation verifies that a
// retired handle whose free a live generation refuses is retried and
// freed by the next successful mutation.
func Test_Core_Replace_FreesParkedHandleOnNextMutation(t *testing.T) {
	backend := &fakeBackend{}
	core := crud.NewCore(backend)

	require.NoError(t, core.Replace("wan", "first"))
	backend.handles[0].refuseOnce = true
	require.NoError(t, core.Replace("wan", "second"))
	require.Equal(t, 1, backend.handles[0].frees)

	require.NoError(t, core.Replace("wan", "third"))
	require.Equal(t, 2, backend.handles[0].frees)
}

// Test_Entry_Free_WithoutHandleIsNil verifies that an entry that holds
// no handle frees nothing and reports success.
func Test_Entry_Free_WithoutHandleIsNil(t *testing.T) {
	require.NoError(t, (&crud.Entry[string]{}).Free())
}

// Test_DeleteError_MapsFailures verifies that each delete failure kind
// reports its own gRPC status.
func Test_DeleteError_MapsFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "nothing published", err: configstore.ErrNotFound, code: codes.NotFound},
		{name: "dataplane refusal", err: ffi.ErrFailedPrecondition, code: codes.FailedPrecondition},
		{name: "anything else", err: errors.New("boom"), code: codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.code, status.Code(crud.DeleteError("wan", tc.err)))
		})
	}
}
