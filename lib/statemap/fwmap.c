#include "fwmap.h"

void *fwmap_func_registry[FWMAP_FUNC_COUNT] = {
	[FWMAP_UNINITIALIZED] = NULL,
	[FWMAP_HASH_FNV1A] = (void *)fwmap_hash_fnv1a,
	[FWMAP_KEY_EQUAL_DEFAULT] = (void *)fwmap_default_key_equal,
	[FWMAP_RAND_DEFAULT] = (void *)fwmap_rand_default,
	[FWMAP_RAND_SECURE] = (void *)fwmap_rand_secure,
	[FWMAP_COPY_KEY_DEFAULT] = (void *)fwmap_default_copy_key,
	[FWMAP_UPDATE_VALUE_DEFAULT] = (void *)fwmap_default_update_value,
	[FWMAP_PROMOTE_VALUE_DEFAULT] = (void *)fwmap_default_promote_value,
	[FWMAP_PROMOTE_VALUE_KEEP_OLD] = (void *)fwmap_promote_value_keep_old,
};

void
fwmap_func_registry_set(fwmap_func_id_t id, void *fn) {
	fwmap_func_registry[id] = fn;
}
