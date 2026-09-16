package pdump_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
)

// fakeModule is a published module whose rings live in Go memory.
type fakeModule struct {
	settings pdump.Settings
	rings    []pdump.Ring
	// opened receives when a stream takes the rings.
	opened chan struct{}
	// refusals is how many frees report the module still referenced.
	refusals atomic.Int32
	freed    atomic.Bool
}

func (m *fakeModule) Rings() []pdump.Ring {
	select {
	case m.opened <- struct{}{}:
	default:
	}
	return m.rings
}

// Free refuses while refusals remain, then poisons the rings.
func (m *fakeModule) Free() error {
	if m.refusals.Add(-1) >= 0 {
		return ffi.ErrStillReferenced
	}
	m.poison()
	m.freed.Store(true)
	return nil
}

// poison writes the rings without synchronization, which the race detector
// reports against a reader still running.
func (m *fakeModule) poison() {
	for _, ring := range m.rings {
		*ring.WriteIdx = 0
		*ring.ReadableIdx = 0
	}
}

// fakeBackend publishes fakeModules and remembers every one it built and
// every name it deleted. An update of a blocked name waits until the test
// releases it.
type fakeBackend struct {
	mu      sync.Mutex
	modules []*fakeModule
	deleted []string
	blocks  map[string]*updateBlock
	// deleteErr, when set, is returned by every delete.
	deleteErr error
}

// updateBlock holds an update of one name inside the backend.
type updateBlock struct {
	entered chan struct{}
	release chan struct{}
}

// blockUpdate makes the next update of the name wait, and returns the
// channel closed once it entered the backend together with its release.
func (m *fakeBackend) blockUpdate(name string) (<-chan struct{}, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.blocks == nil {
		m.blocks = map[string]*updateBlock{}
	}
	block := &updateBlock{entered: make(chan struct{}), release: make(chan struct{})}
	m.blocks[name] = block

	return block.entered, sync.OnceFunc(func() { close(block.release) })
}

func (m *fakeBackend) UpdateModule(name string, settings pdump.Settings) (pdump.Module, error) {
	m.mu.Lock()
	block := m.blocks[name]
	delete(m.blocks, name)
	m.mu.Unlock()
	if block != nil {
		close(block.entered)
		<-block.release
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	module := &fakeModule{
		settings: settings,
		rings: []pdump.Ring{{
			WriteIdx:    new(uint64),
			ReadableIdx: new(uint64),
			Data:        make([]byte, 1024),
		}},
		opened: make(chan struct{}, 1),
	}
	m.modules = append(m.modules, module)
	return module, nil
}

func (m *fakeBackend) DeleteModule(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleted = append(m.deleted, name)
	return m.deleteErr
}

// Deleted returns the names deleted so far, in order.
func (m *fakeBackend) Deleted() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]string(nil), m.deleted...)
}

// Last returns the module published most recently.
func (m *fakeBackend) Last() *fakeModule {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.modules[len(m.modules)-1]
}

// TestShowConfigUnknownConfig verifies that ShowConfig reports NotFound for
// a config name that was never set.
func TestShowConfigUnknownConfig(t *testing.T) {
	service := pdump.NewPdumpService(nil)

	_, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_PdumpService_SetConfig_MergesMaskedFieldsOverPublishedConfig verifies
// that a masked update keeps the fields it does not name and publishes the
// merged settings.
func Test_PdumpService_SetConfig_MergesMaskedFieldsOverPublishedConfig(t *testing.T) {
	backend := &fakeBackend{}
	service := pdump.NewPdumpService(backend)

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name: "capture",
		Config: &pdumppb.Config{
			Filter:   "udp",
			Mode:     2,
			Snaplen:  256,
			RingSize: uint32(2 * datasize.MB),
		},
		UpdateMask: &pdumppb.FieldMask{Paths: []string{"filter", "mode", "snaplen", "ring_size"}},
	})
	require.NoError(t, err)

	_, err = service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:       "capture",
		Config:     &pdumppb.Config{Filter: "tcp"},
		UpdateMask: &pdumppb.FieldMask{Paths: []string{"filter"}},
	})
	require.NoError(t, err)

	want := pdump.Settings{Filter: "tcp", Mode: 2, Snaplen: 256, RingSize: uint32(2 * datasize.MB)}
	require.Equal(t, want, backend.Last().settings)

	response, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
	require.NoError(t, err)
	require.Equal(t, &pdumppb.Config{Filter: "tcp", Mode: 2, Snaplen: 256, RingSize: uint32(2 * datasize.MB)}, response.Config)
}

// fakeStream is a ReadDump stream that drops every record.
type fakeStream struct {
	grpc.ServerStream

	ctx context.Context
}

func (m *fakeStream) Context() context.Context {
	return m.ctx
}

func (m *fakeStream) Send(*pdumppb.Record) error {
	return nil
}

// setFilter applies a filter-only update to the named config.
func setFilter(t *testing.T, service *pdump.PdumpService, name, filter string) {
	t.Helper()

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:       name,
		Config:     &pdumppb.Config{Filter: filter},
		UpdateMask: &pdumppb.FieldMask{Paths: []string{"filter"}},
	})
	require.NoError(t, err)
}

// openStream starts a ReadDump of the name on the context and returns once
// it reads the module's rings, with the channel its result arrives on.
func openStream(t *testing.T, ctx context.Context, service *pdump.PdumpService, name string, module *fakeModule) <-chan error {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		done <- service.ReadDump(&pdumppb.ReadDumpRequest{Name: name}, &fakeStream{ctx: ctx})
	}()

	select {
	case <-module.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("ReadDump did not open the rings")
	}
	return done
}

// Test_PdumpService_SetConfig_EndsStreamsBeforeFreeingReplacedModule verifies
// that an update of a name ends its streams and frees the replaced module
// only after they stopped reading its rings.
func Test_PdumpService_SetConfig_EndsStreamsBeforeFreeingReplacedModule(t *testing.T) {
	backend := &fakeBackend{}
	service := pdump.NewPdumpService(backend)
	setFilter(t, service, "capture", "udp")
	replaced := backend.Last()
	stream := openStream(t, t.Context(), service, "capture", replaced)

	setFilter(t, service, "capture", "tcp")

	select {
	case err := <-stream:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the update did not end the stream")
	}
	require.True(t, replaced.freed.Load(), "the update must free the replaced module")
}

// Test_PdumpService_DeleteConfig_EndsStreamsAndFreesModule verifies that a
// delete ends the name's streams, unpublishes and frees its module, and
// leaves the name unknown.
func Test_PdumpService_DeleteConfig_EndsStreamsAndFreesModule(t *testing.T) {
	backend := &fakeBackend{}
	service := pdump.NewPdumpService(backend)
	setFilter(t, service, "capture", "udp")
	module := backend.Last()
	stream := openStream(t, t.Context(), service, "capture", module)

	_, err := service.DeleteConfig(t.Context(), &pdumppb.DeleteConfigRequest{Name: "capture"})
	require.NoError(t, err)

	select {
	case err := <-stream:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the delete did not end the stream")
	}
	require.Equal(t, []string{"capture"}, backend.Deleted())
	require.True(t, module.freed.Load(), "the delete must free the module")

	_, err = service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_PdumpService_DeleteConfig_Refused verifies that a refused delete maps
// its error kind to the status code and leaves the config in place.
func Test_PdumpService_DeleteConfig_Refused(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{
			name: "referenced by a chain",
			err:  fmt.Errorf("module 'pdump:capture' not found in chain 'chain0': %w", ffi.ErrFailedPrecondition),
			code: codes.FailedPrecondition,
		},
		{
			name: "backend failure",
			err:  errors.New("dp_config_wait_for_gen timed out"),
			code: codes.Internal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{deleteErr: tc.err}
			service := pdump.NewPdumpService(backend)
			setFilter(t, service, "capture", "udp")

			_, err := service.DeleteConfig(t.Context(), &pdumppb.DeleteConfigRequest{Name: "capture"})
			require.Equal(t, tc.code, status.Code(err))

			_, err = service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "capture"})
			require.NoError(t, err)
		})
	}
}

// Test_PdumpService_SetConfig_RefusedFreeReleasesReplacedModuleLater verifies
// that a replaced module whose free was refused is freed by a later update,
// while the module published in its place stays live.
func Test_PdumpService_SetConfig_RefusedFreeReleasesReplacedModuleLater(t *testing.T) {
	backend := &fakeBackend{}
	service := pdump.NewPdumpService(backend)
	setFilter(t, service, "capture", "udp")
	replaced := backend.Last()
	replaced.refusals.Store(1)

	setFilter(t, service, "capture", "tcp")
	published := backend.Last()
	require.False(t, replaced.freed.Load(), "a refused free must leave the module allocated")

	setFilter(t, service, "other", "udp")

	require.True(t, replaced.freed.Load(), "a later update must free the replaced module")
	require.False(t, published.freed.Load(), "the published module must stay live")
}

// Test_PdumpService_Shutdown_StopsReadersBeforeStreamsReturn verifies that a
// stream ended by a shutdown stops reading the rings before its handler
// returns, so the module can release them.
func Test_PdumpService_Shutdown_StopsReadersBeforeStreamsReturn(t *testing.T) {
	backend := &fakeBackend{}
	service := pdump.NewPdumpService(backend)
	setFilter(t, service, "capture", "udp")
	module := backend.Last()
	stream := openStream(t, t.Context(), service, "capture", module)

	service.Shutdown()

	select {
	case err := <-stream:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the shutdown did not end the stream")
	}
	module.poison()
}

// updateFilter applies a filter-only update to the named config and reports
// its result on the returned channel.
func updateFilter(ctx context.Context, service *pdump.PdumpService, name, filter string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := service.SetConfig(ctx, &pdumppb.SetConfigRequest{
			Name:       name,
			Config:     &pdumppb.Config{Filter: filter},
			UpdateMask: &pdumppb.FieldMask{Paths: []string{"filter"}},
		})
		done <- err
	}()
	return done
}

// Test_PdumpService_ShowConfig_DoesNotWaitForPublish verifies that reading a
// config does not wait while an update of that name is inside the backend.
func Test_PdumpService_ShowConfig_DoesNotWaitForPublish(t *testing.T) {
	backend := &fakeBackend{}
	service := pdump.NewPdumpService(backend)
	setFilter(t, service, "capture", "udp")

	entered, release := backend.blockUpdate("capture")
	t.Cleanup(release)
	update := updateFilter(t.Context(), service, "capture", "tcp")
	<-entered

	ctx := t.Context()
	type shownConfig struct {
		response *pdumppb.ShowConfigResponse
		err      error
	}
	shown := make(chan shownConfig, 1)
	go func() {
		response, err := service.ShowConfig(ctx, &pdumppb.ShowConfigRequest{Name: "capture"})
		shown <- shownConfig{response: response, err: err}
	}()

	select {
	case result := <-shown:
		require.NoError(t, result.err)
		require.Equal(t, "udp", result.response.Config.Filter, "the blocked update must stay unpublished")
	case <-time.After(5 * time.Second):
		t.Fatal("ShowConfig waited for the blocked update")
	}

	release()
	require.NoError(t, <-update)
}

// Test_PdumpService_SetConfig_DoesNotSerializeNames verifies that an update of
// one name runs while an update of another name is inside the backend.
func Test_PdumpService_SetConfig_DoesNotSerializeNames(t *testing.T) {
	backend := &fakeBackend{}
	service := pdump.NewPdumpService(backend)

	entered, release := backend.blockUpdate("blocked")
	t.Cleanup(release)
	blocked := updateFilter(t.Context(), service, "blocked", "udp")
	<-entered

	other := updateFilter(t.Context(), service, "other", "tcp")
	select {
	case err := <-other:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("an update of another name waited for the blocked one")
	}

	release()
	require.NoError(t, <-blocked)
}

// Test_PdumpService_ReadDump_ReportsClientCancellation verifies that a stream
// its client abandons ends with the client's cancellation.
func Test_PdumpService_ReadDump_ReportsClientCancellation(t *testing.T) {
	backend := &fakeBackend{}
	service := pdump.NewPdumpService(backend)
	setFilter(t, service, "capture", "udp")

	ctx, cancel := context.WithCancel(t.Context())
	stream := openStream(t, ctx, service, "capture", backend.Last())

	cancel()

	select {
	case err := <-stream:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled client did not end the stream")
	}
}
