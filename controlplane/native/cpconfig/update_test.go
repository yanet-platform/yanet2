//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_UpdateModules_RefusesHarnessWithoutDelivery verifies that an
// update needing a delivery and given none is refused before any lock.
//
// The dataplane runs worker rounds on request. The instance has no
// control-plane zone, so taking its lock would fault.
func Test_UpdateModules_RefusesHarnessWithoutDelivery(t *testing.T) {
	instance := &zone.Instance{Dp: &layout.DpConfig{ExternalWorkerRounds: true}}

	err := cpconfig.UpdateModules(instance, []*layout.CpModule{{}})

	require.ErrorIs(t, err, cpconfig.ErrNeedsDelivery)
}

// Test_UpdateModules_RefusesNilModule verifies that an update holding a
// nil module is refused before the instance is touched.
//
// The instance has no zones, so any access to them would fault.
func Test_UpdateModules_RefusesNilModule(t *testing.T) {
	err := cpconfig.UpdateModules(&zone.Instance{}, []*layout.CpModule{{}, nil})

	require.Error(t, err)
}

// Test_DeleteModule_RefusesNameItsFieldCannotHold verifies that a delete
// naming a module type or name C would cut short is refused before the
// instance is touched.
//
// The instance has no zones, so taking its lock would fault.
func Test_DeleteModule_RefusesNameItsFieldCannotHold(t *testing.T) {
	cases := invalidIdentities(len(layout.CpModule{}.Type), len(layout.CpModule{}.Name), "blackhole", "a")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cpconfig.DeleteModule(&zone.Instance{}, tc.entityType, tc.entityName)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}
