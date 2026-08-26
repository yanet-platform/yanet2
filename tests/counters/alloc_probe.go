package counters_test

// The probes interpose this binary's own dynamic allocations so a test
// can observe both how many allocations a read performs and how many
// stay outstanding after it returns.

/*
#cgo LDFLAGS: -Wl,--wrap=malloc -Wl,--wrap=calloc -Wl,--wrap=strdup -Wl,--wrap=free

#include <malloc.h>
#include <stddef.h>
#include <stdint.h>

extern void *__real_malloc(size_t size);
extern void *__real_calloc(size_t nmemb, size_t size);
extern char *__real_strdup(const char *str);
extern void __real_free(void *ptr);

static size_t alloc_count = 0;
static size_t live_count = 0;
static size_t live_bytes = 0;

static void
track_alloc(void *ptr) {
	__atomic_fetch_add(&alloc_count, 1, __ATOMIC_RELAXED);
	if (ptr == NULL) {
		return;
	}
	__atomic_fetch_add(&live_count, 1, __ATOMIC_RELAXED);
	// Book the usable size, the same figure free books, so the
	// outstanding bytes balance exactly instead of drifting by the
	// allocator's rounding on every short string.
	__atomic_fetch_add(&live_bytes, malloc_usable_size(ptr), __ATOMIC_RELAXED);
}

void *
__wrap_malloc(size_t size) {
	void *ptr = __real_malloc(size);
	track_alloc(ptr);
	return ptr;
}

void *
__wrap_calloc(size_t nmemb, size_t size) {
	void *ptr = __real_calloc(nmemb, size);
	track_alloc(ptr);
	return ptr;
}

char *
__wrap_strdup(const char *str) {
	char *ptr = __real_strdup(str);
	track_alloc(ptr);
	return ptr;
}

void
__wrap_free(void *ptr) {
	if (ptr != NULL) {
		size_t size = malloc_usable_size(ptr);
		__atomic_fetch_sub(&live_count, 1, __ATOMIC_RELAXED);
		__atomic_fetch_sub(&live_bytes, size, __ATOMIC_RELAXED);
	}
	__real_free(ptr);
}

size_t
alloc_count_load(void) {
	return __atomic_load_n(&alloc_count, __ATOMIC_RELAXED);
}

size_t
live_count_load(void) {
	return __atomic_load_n(&live_count, __ATOMIC_RELAXED);
}

size_t
live_bytes_load(void) {
	return __atomic_load_n(&live_bytes, __ATOMIC_RELAXED);
}
*/
import "C"

// allocCount returns the number of malloc/calloc/strdup calls observed so
// far by the probes linked into this test binary.
//
// This is a lower bound covering only this binary's own object files: a
// call made inside a precompiled shared library, such as glibc's internal
// strdup, is not linked against the wrap symbols and stays uncounted.
func allocCount() uint64 {
	return uint64(C.alloc_count_load())
}

// outstandingAllocs is the number of tracked allocations not yet freed
// and the usable bytes they retain, for catching a read that leaves
// blocks behind.
type outstandingAllocs struct {
	count uint64
	bytes uint64
}

func liveOutstanding() outstandingAllocs {
	return outstandingAllocs{
		count: uint64(C.live_count_load()),
		bytes: uint64(C.live_bytes_load()),
	}
}
