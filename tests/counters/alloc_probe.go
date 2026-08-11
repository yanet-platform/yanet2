package counters_test

//#cgo LDFLAGS: -Wl,--wrap=malloc -Wl,--wrap=calloc
/*
#include <stddef.h>

extern void *__real_malloc(size_t size);
extern void *__real_calloc(size_t nmemb, size_t size);

static size_t alloc_count = 0;

void *
__wrap_malloc(size_t size) {
	__atomic_fetch_add(&alloc_count, 1, __ATOMIC_RELAXED);
	return __real_malloc(size);
}

void *
__wrap_calloc(size_t nmemb, size_t size) {
	__atomic_fetch_add(&alloc_count, 1, __ATOMIC_RELAXED);
	return __real_calloc(nmemb, size);
}

size_t
alloc_count_load(void) {
	return __atomic_load_n(&alloc_count, __ATOMIC_RELAXED);
}
*/
import "C"

// allocCount returns the number of malloc/calloc calls observed so far by
// the __wrap_malloc/__wrap_calloc probes linked into this test binary.
//
// This is a lower bound covering only this binary's own object files: a
// call made inside a precompiled shared library, such as glibc's internal
// strdup, is not linked against the wrap symbols and stays uncounted.
func allocCount() uint64 {
	return uint64(C.alloc_count_load())
}
