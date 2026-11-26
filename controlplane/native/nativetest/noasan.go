//go:build linux && (amd64 || arm64) && !asan

package nativetest

import (
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
)

// sanitized tells that this process holds no sanitizer marks to send the
// helper.
const sanitized = false

// Unpoison does nothing without AddressSanitizer.
func Unpoison([]byte) {}

// markFreeBlocks does nothing without AddressSanitizer.
func markFreeBlocks(*layout.CpConfig, []byte) error {
	return nil
}

// unaddressableRuns finds nothing without AddressSanitizer.
func unaddressableRuns([]byte, uint64) []helperproto.Range {
	return nil
}
