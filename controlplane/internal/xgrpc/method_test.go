package xgrpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ParseFullMethod(t *testing.T) {
	service, method, err := ParseFullMethod("/routepb.Route/InsertRoute")

	require.NoError(t, err)
	assert.Equal(t, "routepb.Route", service)
	assert.Equal(t, "InsertRoute", method)
}

func Test_ParseFullMethodNoLeadingSlash(t *testing.T) {
	service, method, err := ParseFullMethod("routepb.Route/InsertRoute")

	require.Error(t, err)
	assert.Equal(t, "", service)
	assert.Equal(t, "", method)
}

func Test_ParseFullMethodNoMethod(t *testing.T) {
	service, method, err := ParseFullMethod("/routepb.Route")

	require.Error(t, err)
	assert.Equal(t, "", service)
	assert.Equal(t, "", method)
}
