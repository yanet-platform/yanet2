package ffi

//#include "sigbus.h"
import "C"

import (
	"fmt"
	"sync"
)

var installSIGBUSHandler = sync.OnceValue(func() error {
	if result, err := C.yanet_install_sigbus_handler(); result != 0 {
		return fmt.Errorf("failed to install SIGBUS handler: %w", err)
	}
	return nil
})
