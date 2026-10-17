#include "lpm_image.h"

#include <stdlib.h>
#include <string.h>

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "modules/decap/dataplane/config.h"

#define TK_ALIGN 64
#define TK_ROUND_UP(x) (((x) + TK_ALIGN - 1) & ~(size_t)(TK_ALIGN - 1))

// Allocator state at the start of every image.
struct tk_header {
	struct block_allocator allocator;
	struct memory_context context;
};

static struct decap_module_config *
tk_config(const struct tk_image *image) {
	return (struct decap_module_config *)(image->base + image->config_offset
	);
}

static struct lpm *
tk_lpm(const struct tk_image *image, int v6) {
	struct decap_module_config *config = tk_config(image);
	return v6 ? &config->prefixes6 : &config->prefixes4;
}

struct tk_image *
tk_image_new(size_t size) {
	size_t config_offset = TK_ROUND_UP(sizeof(struct tk_header));
	size_t arena_offset =
		TK_ROUND_UP(config_offset + sizeof(struct decap_module_config));
	size = TK_ROUND_UP(size);
	if (size <= arena_offset) {
		return NULL;
	}

	struct tk_image *image = calloc(1, sizeof(*image));
	if (image == NULL) {
		return NULL;
	}
	image->base = aligned_alloc(TK_ALIGN, size);
	if (image->base == NULL) {
		free(image);
		return NULL;
	}
	memset(image->base, 0, size);
	image->size = size;
	image->config_offset = config_offset;

	struct tk_header *header = (struct tk_header *)image->base;
	block_allocator_init(&header->allocator);
	block_allocator_put_arena(
		&header->allocator,
		image->base + arena_offset,
		size - arena_offset
	);
	memory_context_init(&header->context, "tk_image", &header->allocator);

	struct decap_module_config *config = tk_config(image);
	if (lpm_init(&config->prefixes4, &header->context, "prefixes4") ||
	    lpm_init(&config->prefixes6, &header->context, "prefixes6")) {
		tk_image_free(image);
		return NULL;
	}
	return image;
}

void
tk_image_free(struct tk_image *image) {
	if (image == NULL) {
		return;
	}
	free(image->base);
	free(image);
}

int
tk_image_insert(
	struct tk_image *image,
	int v6,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	return lpm_insert(tk_lpm(image, v6), v6 ? 16 : 4, from, to, value);
}

uint32_t
tk_image_lookup(const struct tk_image *image, int v6, const uint8_t *key) {
	return lpm_lookup(tk_lpm(image, v6), v6 ? 16 : 4, key);
}

void
tk_image_lookup_many(
	const struct tk_image *image,
	int v6,
	const uint8_t *keys,
	size_t count,
	uint32_t *results
) {
	const struct lpm *lpm = tk_lpm(image, v6);
	uint8_t key_size = v6 ? 16 : 4;
	for (size_t i = 0; i < count; ++i) {
		results[i] = lpm_lookup(lpm, key_size, keys + i * key_size);
	}
}
