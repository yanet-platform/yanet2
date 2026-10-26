#pragma once

// Debug invariants of the generic registry, for test suites driving
// the raw registry API.
//
// Debug and sanitized builds of the library enable these checks
// through the registry header's flag and pay a whole-registry rescan
// after every mutation; release mutations stay constant time. Tests
// include this file directly, because their own translation units
// compile the inline mutations without the library's flag.

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>

#include "lib/controlplane/config/registry.h"

static inline int
registry_sanitize_slot_eq(uint32_t value, const void *data) {
	return value != *(const uint32_t *)data;
}

// Assert the side index resolves every live item to its own slot and
// counts exactly the live slots.
//
// The scan exists only to feed assertions: builds with assertions
// compiled out reduce it to nothing, so release mutations stay
// constant time instead of rescanning the registry.
static inline void
registry_sanitize_index(
	struct registry *registry, const struct registry_keys *keys
) {
#ifndef NDEBUG
	uint64_t live = 0;
	for (uint64_t slot = 0; slot < registry->capacity; ++slot) {
		struct registry_item *item = registry_get(registry, slot);
		if (item == NULL) {
			continue;
		}

		if (keys != NULL) {
			uint32_t expected = (uint32_t)slot;
			uint32_t found = hash_index_lookup(
				&registry->key_index,
				registry_hash_fold(keys->item_hash(item)),
				registry_sanitize_slot_eq,
				&expected
			);
			assert(found == expected);
		}
		live += 1;
	}

	// The free chain must thread exactly the empty slots: no slot
	// twice, every chained slot reading as empty, and every empty
	// slot on the chain. The check covers unindexed registries too.
	unsigned char *on_chain = calloc(registry->capacity, 1);
	assert(on_chain != NULL);
	uint64_t chained = 0;
	struct registry_item *node = ADDR_OF(&registry->free_list);
	struct registry_item **items = ADDR_OF(&registry->items);
	while (node != NULL) {
		uint64_t slot =
			((uintptr_t)node - (uintptr_t)items) / sizeof(*items);
		assert(slot < registry->capacity);
		assert(!on_chain[slot]);
		on_chain[slot] = 1;
		++chained;
		node = ADDR_OF((struct registry_item **)node);
	}
	assert(chained + live == registry->capacity);
	for (uint64_t slot = 0; slot < registry->capacity; ++slot) {
		assert(on_chain[slot] == (registry_get(registry, slot) == NULL)
		);
	}
	free(on_chain);

	if (keys != NULL) {
		assert((uint64_t)registry->key_index.count == live);
	}
#else
	(void)registry;
	(void)keys;
#endif
}
