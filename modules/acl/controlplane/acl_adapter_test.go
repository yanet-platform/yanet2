package acl_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

// noopBackend is a minimal Backend implementation for tests that only
// exercise ConfigsUsingMap lookup without compiling rules or publishing
// handles.
type noopBackend struct{}

func (m *noopBackend) NewModule(string) (acl.ModuleHandle, error) { return nil, nil }
func (m *noopBackend) UpdateModule(acl.ModuleHandle) error        { return nil }
func (m *noopBackend) UpdateModules([]ffi.ModuleConfig) error     { return nil }
func (m *noopBackend) DeleteModule(string) error                  { return nil }
func (m *noopBackend) MemoryBytes() uint64                        { return 0 }
func (m *noopBackend) DPConfig() *ffi.DPConfig                    { return nil }

// TestACLConfigsKeyedByMapName is the positive regression test that ACL
// configs are keyed by their fwtable names directly.
//
// Pre-decoupling, the fwstate linkage machinery looked up linked ACL
// configs by FwStateConfig.LinkKey() — a value that silently diverged
// from the sync-config name when map_name was set. The new design removes
// that indirection: ACL configs reference fwstate-maps by name and the
// MapConsumer contract keys the lookup on exactly that name. This test
// pins the storage-vs-lookup key equivalence so the divergence cannot
// come back.
func TestACLConfigsKeyedByMapName(t *testing.T) {
	svc := acl.NewACLService(&noopBackend{})

	// Two ACL configs reference the same map pair; a third references a
	// different pair; a fourth has no maps at all.
	svc.PutConfigForTest("acl1", "mapA-v4", "mapA-v6")
	svc.PutConfigForTest("acl2", "mapA-v4", "mapA-v6")
	svc.PutConfigForTest("acl3", "mapB-v4", "mapB-v6")
	svc.PutConfigForTest("acl4", "", "")

	consumer := acl.NewACLMapConsumer(svc)
	require.ElementsMatch(t,
		[]string{"acl1", "acl2"},
		consumer.ConfigsUsingMap("mapA-v4"),
		"configs referencing mapA-v4 are exactly acl1 and acl2",
	)
	require.ElementsMatch(t,
		[]string{"acl1", "acl2"},
		consumer.ConfigsUsingMap("mapA-v6"),
		"configs referencing mapA-v6 are exactly acl1 and acl2",
	)
	require.ElementsMatch(t,
		[]string{"acl3"},
		consumer.ConfigsUsingMap("mapB-v4"),
		"configs referencing mapB-v4 are exactly acl3",
	)
	require.Empty(t,
		consumer.ConfigsUsingMap("nonexistent"),
		"unknown map name yields no configs",
	)
}

// TestACLConfigsUsingMapConcurrent verifies that concurrent
// ConfigsUsingMap (the public, self-locking path shared by DeleteMap's
// consumer check) does not race against config mutations under
// go test -race.
func TestACLConfigsUsingMapConcurrent(t *testing.T) {
	svc := acl.NewACLService(&noopBackend{})
	svc.PutConfigForTest("acl1", "mapA-v4", "mapA-v6")
	svc.PutConfigForTest("acl2", "mapA-v4", "mapA-v6")
	svc.PutConfigForTest("acl3", "", "")

	consumer := acl.NewACLMapConsumer(svc)

	var group errgroup.Group
	for range 16 {
		group.Go(func() error {
			got := consumer.ConfigsUsingMap("mapA-v4")
			require.ElementsMatch(t, []string{"acl1", "acl2"}, got)
			return nil
		})
	}
	require.NoError(t, group.Wait())
}
