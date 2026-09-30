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

// Test_CreateRingRequest_Validate checks the capacity rules.
//
// Test_ValidateRingName covers the name rules.
func Test_CreateRingRequest_Validate(t *testing.T) {
	cases := []struct {
		name     string
		capacity uint64
		message  string
	}{
		{name: "zero", capacity: 0, message: "capacity 0 must be at least 8"},
		{name: "below one record frame", capacity: 4, message: "capacity 4 must be at least 8"},
		{name: "not a power of two", capacity: 12, message: "capacity 12 must be a power of two"},
		{
			name:     "beyond 32 bits",
			capacity: 1 << 32,
			message:  "capacity 4294967296 exceeds the maximum representable value 4294967295",
		},
		{name: "exactly one record frame", capacity: 8},
		{name: "large power of two", capacity: 1 << 30},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&ringpb.CreateRingRequest{Name: "ring0", Capacity: tc.capacity}).Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_CreateRingRequest_ValidatePublishBatch checks the publish batch
// range.
//
// A value of 0 means the default.
func Test_CreateRingRequest_ValidatePublishBatch(t *testing.T) {
	cases := []struct {
		name    string
		batch   uint32
		message string
	}{
		{name: "unset", batch: 0},
		{name: "one record", batch: 1},
		{name: "maximum", batch: ringpb.MaxPublishBatch},
		{
			name:    "above maximum",
			batch:   ringpb.MaxPublishBatch + 1,
			message: "publish_batch 1025 must be at most 1024 records",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &ringpb.CreateRingRequest{Name: "ring0", Capacity: 64, PublishBatch: tc.batch}
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

// Test_Requests_ValidateName checks that every request applies the name
// rules to its name field.
//
// A nil request is checked too.
func Test_Requests_ValidateName(t *testing.T) {
	cases := map[string]struct {
		build func(name string) interface{ Validate() error }
		null  interface{ Validate() error }
	}{
		"create": {
			build: func(name string) interface{ Validate() error } {
				return &ringpb.CreateRingRequest{Name: name, Capacity: 8}
			},
			null: (*ringpb.CreateRingRequest)(nil),
		},
		"show": {
			build: func(name string) interface{ Validate() error } { return &ringpb.ShowRingRequest{Name: name} },
			null:  (*ringpb.ShowRingRequest)(nil),
		},
		"delete": {
			build: func(name string) interface{ Validate() error } { return &ringpb.DeleteRingRequest{Name: name} },
			null:  (*ringpb.DeleteRingRequest)(nil),
		},
	}

	for request, tc := range cases {
		t.Run(request, func(t *testing.T) {
			require.NoError(t, tc.build("ring0").Validate())
			require.EqualError(t, tc.build("a\x00b").Validate(), "name must not contain NUL")
			require.EqualError(t, tc.null.Validate(), "name is required")
		})
	}
}
