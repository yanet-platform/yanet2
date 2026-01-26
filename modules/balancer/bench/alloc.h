#pragma once

#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

struct allocator {
	size_t allocated;
	size_t size;
	void *arena;
};

void
allocator_init(struct allocator *alloc, void *arena, size_t size);

uint8_t *
allocator_alloc(struct allocator *alloc, size_t align, size_t size);
