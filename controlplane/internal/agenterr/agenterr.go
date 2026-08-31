// Package agenterr classifies pipeline and function mutation failures
// into gRPC statuses.
//
// No error code crosses the FFI boundary, only formatted text, so
// classification anchors on that flat message instead of a code.
package agenterr

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ClassifyDelete maps a Delete RPC's backend failure to a gRPC status.
//
// A trailing "not found" names the deleted entity itself; the same
// wording continuing further names a live in-use refusal instead and
// stays Internal.
func ClassifyDelete(err error) error {
	return classify(err, strings.HasSuffix(err.Error(), "' not found"))
}

// ClassifyUpdate maps an Update RPC's backend failure to a gRPC status.
//
// A pipeline update and a function update reach the same underlying
// validation, so a missing referenced function or a missing chain
// module both surface as NotFound regardless of which RPC ran; every
// other failure stays Internal.
func ClassifyUpdate(err error) error {
	msg := err.Error()
	notFound := strings.Contains(msg, "' not found for pipeline '") ||
		strings.Contains(msg, "' not found in chain '")
	return classify(err, notFound)
}

func classify(err error, notFound bool) error {
	if notFound {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
