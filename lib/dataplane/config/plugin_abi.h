#pragma once

#include <stddef.h>

// One ABI table row: an entity's name and the sha256 hash of its declaration.
struct yanet_abi_entity {
	const char *name;
	const char *hash;
};

// Symbol a plugin exports for its own ABI table, from abigen "-plugin" mode.
#define YANET_MODULE_ABI_ENTITIES_SYMBOL "yanet_module_abi_entities_v1"
// Symbol a plugin exports for the count of YANET_MODULE_ABI_ENTITIES_SYMBOL.
#define YANET_MODULE_ABI_COUNT_SYMBOL "yanet_module_abi_count_v1"

// The dataplane binary's own ABI table, sorted by name.
extern const struct yanet_abi_entity yanet_dataplane_abi_entities_v1[];
// Number of entries in yanet_dataplane_abi_entities_v1.
extern const size_t yanet_dataplane_abi_count_v1;
