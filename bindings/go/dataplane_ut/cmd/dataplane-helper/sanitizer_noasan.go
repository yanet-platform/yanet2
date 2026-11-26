//go:build linux && (amd64 || arm64) && !asan

package main

import (
	"errors"

	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
)

// errNoSanitizer refuses a sanitizer fault in a build without one.
var errNoSanitizer = errors.New("a sanitizer report needs an AddressSanitizer build")

// readPoisonedFromC refuses: no sanitizer reports the read.
func readPoisonedFromC() error {
	return errNoSanitizer
}

// readPast refuses: no sanitizer reports the read.
func readPast([]byte, uint64) error {
	return errNoSanitizer
}

// poison does nothing without AddressSanitizer.
func poison([]byte, uint64, []helperproto.Range) {}
