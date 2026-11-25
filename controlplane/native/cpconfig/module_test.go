//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// invalidName is a value a C character field cannot hold whole.
type invalidName struct {
	name  string
	value string
}

// invalidNames returns the values a C character field of that size cannot
// hold whole: one without room for its terminator and one with an inner
// NUL.
func invalidNames(size int) []invalidName {
	return []invalidName{
		{"value without room for its terminator", strings.Repeat("n", size)},
		{"value with an inner NUL", "probe\x00other"},
	}
}

// identity is an entity's type and name, as a test case names them.
type identity struct {
	name       string
	entityType string
	entityName string
}

// invalidIdentities returns the types and names fields of those sizes cannot
// hold whole, each paired with a valid other half.
func invalidIdentities(typeSize, nameSize int, validType, validName string) []identity {
	var cases []identity
	for _, invalid := range invalidNames(typeSize) {
		cases = append(cases, identity{"type: " + invalid.name, invalid.value, validName})
	}
	for _, invalid := range invalidNames(nameSize) {
		cases = append(cases, identity{"name: " + invalid.name, validType, invalid.value})
	}
	return cases
}

// Test_NewBlackhole_RefusesNameItsFieldCannotHold verifies that a name C
// would cut short is refused before the agent is touched.
//
// The owner has no agent, so any allocation from it would fault.
func Test_NewBlackhole_RefusesNameItsFieldCannotHold(t *testing.T) {
	for _, tc := range invalidNames(len(layout.CpModule{}.Name)) {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpconfig.NewBlackhole(&agent.Agent{}, tc.value)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}

// Test_LinkDevice_RefusesNameItsFieldCannotHold verifies that a device
// name C would cut short is refused before the module is touched.
//
// The module has no memory context, so any allocation from it would
// fault.
func Test_LinkDevice_RefusesNameItsFieldCannotHold(t *testing.T) {
	for _, tc := range invalidNames(len(layout.CpModuleDevice{}.Name)) {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpconfig.LinkDevice(&layout.CpModule{}, tc.value)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}
