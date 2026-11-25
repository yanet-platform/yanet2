//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// invalidObjectIdentities returns object types and names their fields
// cannot hold whole.
func invalidObjectIdentities() []identity {
	return invalidIdentities(len(layout.CpObject{}.Type), len(layout.CpObject{}.Name), "ring", "state")
}

// Test_InitObject_RefusesNameItsFieldCannotHold verifies that an object
// type or name C would cut short is refused before the header or the
// agent is touched.
//
// The owner has no instance, so a type lookup would fault.
func Test_InitObject_RefusesNameItsFieldCannotHold(t *testing.T) {
	for _, tc := range invalidObjectIdentities() {
		t.Run(tc.name, func(t *testing.T) {
			object := layout.CpObject{DpObjectIdx: 1}

			err := cpconfig.InitObject(&object, &agent.Agent{}, tc.entityType, tc.entityName)

			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
			require.Equal(t, uint64(1), object.DpObjectIdx, "the header is untouched")
		})
	}
}

// Test_LinkObject_RefusesNameItsFieldCannotHold verifies that a linked
// object type or name C would cut short is refused before the module is
// touched.
//
// The module has no memory context, so any allocation from it would
// fault.
func Test_LinkObject_RefusesNameItsFieldCannotHold(t *testing.T) {
	cases := invalidIdentities(len(layout.CpModuleObject{}.Type), len(layout.CpModuleObject{}.Name), "ring", "state")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpconfig.LinkObject(&layout.CpModule{}, tc.entityType, tc.entityName)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}

// Test_UpdateObjects_RefusesNilObject verifies that an update holding a
// nil object is refused before the instance is touched.
//
// The instance has no zones, so any access to them would fault.
func Test_UpdateObjects_RefusesNilObject(t *testing.T) {
	err := cpconfig.UpdateObjects(&zone.Instance{}, []*layout.CpObject{{}, nil})

	require.Error(t, err)
}

// Test_DeleteObject_RefusesNameItsFieldCannotHold verifies that a delete
// naming an object type or name C would cut short is refused before the
// instance is touched.
//
// The instance has no zones, so taking its lock would fault.
func Test_DeleteObject_RefusesNameItsFieldCannotHold(t *testing.T) {
	for _, tc := range invalidObjectIdentities() {
		t.Run(tc.name, func(t *testing.T) {
			err := cpconfig.DeleteObject(&zone.Instance{}, tc.entityType, tc.entityName)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}
