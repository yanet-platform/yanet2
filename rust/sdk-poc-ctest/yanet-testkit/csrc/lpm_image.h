#pragma once

// Position-independent decap configuration images built by the real C code.
//
// An image is one zeroed, 64-byte aligned allocation holding a block
// allocator, a root memory context, a decap module configuration and the
// arena its LPM pages come from. Every pointer inside is self-relative, so
// a byte copy of the image at another address is an equivalent image; the
// Rust tests rely on that to remap configurations.

#include <stddef.h>
#include <stdint.h>

struct tk_image {
	uint8_t *base;
	size_t size;
	size_t config_offset;
};

// Builds an empty image of the given size; NULL on allocation failure.
struct tk_image *
tk_image_new(size_t size);

void
tk_image_free(struct tk_image *image);

// Inserts the inclusive big-endian range [from, to] into the IPv4 (v6 == 0)
// or IPv6 tree with the C lpm_insert; returns 0 or -1.
int
tk_image_insert(
	struct tk_image *image,
	int v6,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
);

// Looks a key up with the C lpm_lookup.
uint32_t
tk_image_lookup(const struct tk_image *image, int v6, const uint8_t *key);

// Looks up count consecutive keys with the inlined C lpm_lookup, writing one
// result per key; the C side of the lookup benchmark.
void
tk_image_lookup_many(
	const struct tk_image *image,
	int v6,
	const uint8_t *keys,
	size_t count,
	uint32_t *results
);
