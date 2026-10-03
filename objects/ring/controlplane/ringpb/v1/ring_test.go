package ringpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	ringpb "github.com/yanet-platform/yanet2/objects/ring/controlplane/ringpb/v1"
)

// Test_ValidateRingName checks the C object-name rules.
//
// It also checks that the error names the caller's field.
func Test_ValidateRingName(t *testing.T) {
	cases := []struct {
		name     string
		field    string
		ringName string
		message  string
	}{
		{name: "empty", field: "name", message: "name is required"},
		{name: "contains NUL", field: "name", ringName: "a\x00b", message: "name must not contain NUL"},
		{
			name:     "at byte limit",
			field:    "name",
			ringName: strings.Repeat("a", ringpb.MaxRingNameLen),
			message:  "name must be shorter than 80 bytes",
		},
		{name: "longest accepted", field: "name", ringName: strings.Repeat("a", ringpb.MaxRingNameLen-1)},
		{name: "caller's field", field: "ring_name", message: "ring_name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ringpb.ValidateRingName(tc.field, tc.ringName)
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_CreateRingRequest_Validate checks the name, capacity and publish
// batch rules of a create.
//
// Test_ValidateRingName covers the name rules in detail. A publish batch of
// 0 means the default.
func Test_CreateRingRequest_Validate(t *testing.T) {
	cases := []struct {
		name     string
		ringName string
		capacity uint64
		batch    uint32
		message  string
	}{
		{name: "bad name", ringName: "a\x00b", capacity: 64, message: "name must not contain NUL"},
		{name: "zero capacity", capacity: 0, message: "capacity 0 must be at least 8"},
		{name: "below one record frame", capacity: 4, message: "capacity 4 must be at least 8"},
		{name: "not a power of two", capacity: 12, message: "capacity 12 must be a power of two"},
		{
			name:     "beyond 32 bits",
			capacity: 1 << 32,
			message:  "capacity 4294967296 exceeds the maximum representable value 4294967295",
		},
		{name: "exactly one record frame", capacity: 8},
		{name: "large power of two", capacity: 1 << 30},
		{name: "unset publish batch", capacity: 64, batch: 0},
		{name: "one record batch", capacity: 64, batch: 1},
		{name: "maximum publish batch", capacity: 64, batch: ringpb.MaxPublishBatch},
		{
			name:     "publish batch above maximum",
			capacity: 64,
			batch:    ringpb.MaxPublishBatch + 1,
			message:  "publish_batch 1025 must be at most 1024 records",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ringName := tc.ringName
			if ringName == "" {
				ringName = "ring0"
			}
			req := &ringpb.CreateRingRequest{Name: ringName, Capacity: tc.capacity, PublishBatch: tc.batch}
			err := req.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_CreateRingRequest_PublishBatchOrDefault checks that an unset publish
// batch becomes the default and a set one is kept.
func Test_CreateRingRequest_PublishBatchOrDefault(t *testing.T) {
	require.Equal(t, uint32(ringpb.DefaultPublishBatch), (&ringpb.CreateRingRequest{}).PublishBatchOrDefault())
	require.Equal(t, uint32(32), (&ringpb.CreateRingRequest{PublishBatch: 32}).PublishBatchOrDefault())
}

// Test_ShowAndDeleteRequests_ValidateName checks that show and delete apply
// the name rules to their name field.
func Test_ShowAndDeleteRequests_ValidateName(t *testing.T) {
	require.EqualError(t, (&ringpb.ShowRingRequest{Name: "a\x00b"}).Validate(), "name must not contain NUL")
	require.EqualError(t, (&ringpb.DeleteRingRequest{Name: "a\x00b"}).Validate(), "name must not contain NUL")
}
