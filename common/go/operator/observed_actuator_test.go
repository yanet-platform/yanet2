package operator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// fakeActuator is an Actuator that returns a fixed error.
type fakeActuator struct {
	err error
}

func (m *fakeActuator) Apply(_ context.Context, _ struct{}) error {
	return m.err
}

func (m *fakeActuator) Close() error {
	return nil
}

func TestObservedActuator_PropagatesInnerError(t *testing.T) {
	inner := &fakeActuator{err: errors.New("apply failed")}

	var (
		observedID  string
		observedErr error
	)
	callback := func(id string, err error) {
		observedID = id
		observedErr = err
	}

	oa := operator.NewObservedActuator(inner, "gw-test", callback)
	err := oa.Apply(t.Context(), struct{}{})

	require.Error(t, err)
	assert.Equal(t, inner.err, err, "inner error must be returned unchanged")
	assert.Equal(t, "gw-test", observedID)
	assert.Equal(t, inner.err, observedErr)
}

func TestObservedActuator_NilErrorOnSuccess(t *testing.T) {
	inner := &fakeActuator{err: nil}

	var callbackErr error
	oa := operator.NewObservedActuator(inner, "gw-ok", func(_ string, err error) {
		callbackErr = err
	})

	err := oa.Apply(t.Context(), struct{}{})
	require.NoError(t, err)
	assert.NoError(t, callbackErr)
}

func TestObservedActuator_Close_DelegatesToInner(t *testing.T) {
	inner := &fakeActuator{}
	oa := operator.NewObservedActuator(inner, "gw-close", func(_ string, _ error) {})
	assert.NoError(t, oa.Close())
}
