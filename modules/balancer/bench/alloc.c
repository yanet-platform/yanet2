#include "alloc.h"

void
allocator_init(struct allocator *alloc, void *arena, size_t size) {
	alloc->arena = arena;
	alloc->size = size;
	alloc->allocated = 0;
}

uint8_t *
allocator_alloc(struct allocator *alloc, size_t align, size_t size) {
	size_t shift = 0;
	uintptr_t start = (uintptr_t)alloc->arena + alloc->allocated;
	if (start % align != 0) {
		shift = align - start % align;
	}
	size += shift;
	if (alloc->allocated + size > alloc->size) {
		return NULL;
	}
	uint8_t *ptr = (uint8_t *)alloc->arena + alloc->allocated;
	alloc->allocated += size;
	return ptr + shift;
}