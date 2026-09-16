package ynpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_CountersByTagsRequest_Validate verifies that query and tag errors use
// their repeated field paths before shared-memory access is possible.
func Test_CountersByTagsRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.CountersByTagsRequest
		message string
	}{
		{name: "nil request"},
		{name: "empty request", request: &ynpb.CountersByTagsRequest{}},
		{
			name: "query at limit",
			request: &ynpb.CountersByTagsRequest{
				Query: make([]string, 64),
			},
		},
		{
			name: "query over limit",
			request: &ynpb.CountersByTagsRequest{
				Query: make([]string, 65),
			},
			message: "query carries 65 patterns, at most 64 are accepted",
		},
		{
			name: "query contains NUL at repeated index",
			request: &ynpb.CountersByTagsRequest{
				Query: []string{"counter_.*", "rx\x00.*"},
			},
			message: "query[1] must not contain NUL",
		},
		{
			name: "tag key contains NUL at repeated index",
			request: &ynpb.CountersByTagsRequest{
				Tags: []*ynpb.CounterTag{{Key: "device\x00name"}},
			},
			message: "tags[0]: key must not contain NUL",
		},
		{
			name: "tag value reaches buffer limit",
			request: &ynpb.CountersByTagsRequest{
				Tags: []*ynpb.CounterTag{{
					Value: strings.Repeat("v", ynpb.MaxCounterTagValueLen),
				}},
			},
			message: "tags[0]: value must be shorter than 80 bytes",
		},
		{
			name: "valid tag at usable limits",
			request: &ynpb.CountersByTagsRequest{
				Tags: []*ynpb.CounterTag{{
					Key:   strings.Repeat("k", ynpb.MaxCounterTagKeyLen-1),
					Value: strings.Repeat("v", ynpb.MaxCounterTagValueLen-1),
				}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_CounterTag_Validate verifies that key and value NUL and length rules
// are reported relative to the tag message.
func Test_CounterTag_Validate(t *testing.T) {
	cases := []struct {
		name    string
		tag     *ynpb.CounterTag
		message string
	}{
		{name: "nil tag", tag: nil},
		{name: "empty tag", tag: &ynpb.CounterTag{}},
		{
			name:    "key contains NUL",
			tag:     &ynpb.CounterTag{Key: "kind\x00suffix"},
			message: "key must not contain NUL",
		},
		{
			name: "key reaches buffer limit",
			tag: &ynpb.CounterTag{
				Key: strings.Repeat("k", ynpb.MaxCounterTagKeyLen),
			},
			message: "key must be shorter than 80 bytes",
		},
		{
			name:    "value contains NUL",
			tag:     &ynpb.CounterTag{Value: "device\x00suffix"},
			message: "value must not contain NUL",
		},
		{
			name: "value reaches buffer limit",
			tag: &ynpb.CounterTag{
				Value: strings.Repeat("v", ynpb.MaxCounterTagValueLen),
			},
			message: "value must be shorter than 80 bytes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tag.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
