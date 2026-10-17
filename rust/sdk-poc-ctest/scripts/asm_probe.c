// C counterparts of the Rust assembly probes.

#include "common/lpm.h"
#include "common/memory_address.h"

void *
c_resolve(void *root, void **slot) {
	(void)root;
	return ADDR_OF(slot);
}

void *
c_resolve_nonnull(void *root, void **slot) {
	(void)root;
	return ADDR_OF_NONNULL(slot);
}

uint32_t
c_lookup6(const struct lpm *lpm, const uint8_t *key) {
	return lpm_lookup(lpm, 16, key);
}
