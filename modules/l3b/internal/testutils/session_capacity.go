package testutils

/*
#cgo CFLAGS: -I../../../../
#include <errno.h>
#include "lib/l3state/l3state.h"

static int l3b_test_session_capacity(uint32_t index_size) {
	struct block_allocator allocator;
	struct memory_context context;
	block_allocator_init(&allocator);
	memory_context_init(&context, "capacity", &allocator);
	fwmap_config_t config = {0};
	l3s_config(1, index_size, 0, &config);
	errno = 0;
	fwmap_t *map = fwmap_new(&config, &context);
	int result = map == NULL ? errno : 0;
	if (map != NULL) {
		fwmap_free(map, &context);
	}
	memory_context_fini(&context);
	block_allocator_fini(&allocator);
	return result;
}
*/
import "C"

import "syscall"

// SessionCapacityErrno reports native admission using an empty allocator.
func SessionCapacityErrno(indexSize uint32) syscall.Errno {
	return syscall.Errno(C.l3b_test_session_capacity(C.uint32_t(indexSize)))
}
