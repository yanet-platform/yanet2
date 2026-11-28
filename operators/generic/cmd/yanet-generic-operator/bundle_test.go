package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Test_Bundle_LinksTheStatefulNat64Service verifies that an instance
// spells the stateful NAT64 service without naming a descriptor set.
func Test_Bundle_LinksTheStatefulNat64Service(t *testing.T) {
	name := protoreflect.FullName(
		"modules.nat64s.controlplane.nat64spb.v1.NAT64sService",
	)

	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
	require.NoError(t, err)
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	require.True(t, ok, "%s must name a service", name)
	require.NotNil(
		t,
		service.Methods().ByName("UpdateConfig"),
		"UpdateConfig must be among the service's methods",
	)
}
