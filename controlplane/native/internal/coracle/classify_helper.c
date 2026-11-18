// The classify helpers of the module SDK - the join among them - built
// into the oracle with every heap allocation they make counted, so a
// native compile is held to the heap the C one takes beside shared memory.
#include <stdint.h>
#include <stdlib.h>

// The bytes the helpers requested from the heap, each growth counted
// whole as Go counts a grown slice.
uint64_t coracle_heap_bytes;

static void *
coracle_malloc(size_t size) {
	coracle_heap_bytes += size;
	return malloc(size);
}

static void *
coracle_calloc(size_t count, size_t size) {
	coracle_heap_bytes += count * size;
	return calloc(count, size);
}

static void *
coracle_realloc(void *block, size_t size) {
	coracle_heap_bytes += size;
	return realloc(block, size);
}

#define malloc coracle_malloc
#define calloc coracle_calloc
#define realloc coracle_realloc

#include "lib/classify/compiler/helper.c"
