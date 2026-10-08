// C twins of the Rust probes in yanet-sdk/examples/asm_probe.rs.
#include "common/lpm.h"

struct lpm_page *
c_probe_addr_of_nonnull(struct lpm_page **slot) {
	return ADDR_OF_NONNULL(slot);
}

uint32_t
c_probe_lookup4(const struct lpm *lpm, const uint8_t *key) {
	return lpm_lookup(lpm, 4, key);
}
