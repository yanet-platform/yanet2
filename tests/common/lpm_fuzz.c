/*
 * Fuzzes the LPM range insert against the reference walk it replaced.
 *
 * The first byte picks the key size, the second the arena budget of each
 * tree, zero for the largest; the rest is a run of ranges, each a start, an
 * end and a two-byte value.
 */

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "lpm_reference.h"

#define FUZZ_MAX_RANGES 8
#define FUZZ_BUDGET_STEP (16 << 10)

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (size < 2) {
		return 0;
	}
	uint8_t key_size = (uint8_t)(1 + data[0] % LPM_KEY_SIZE_MAX);
	size_t budget = data[1] == 0 ? LPM_REFERENCE_ARENA
				     : data[1] * (size_t)FUZZ_BUDGET_STEP;
	size_t count = (size - 2) / (2u * key_size + 2);
	if (count > FUZZ_MAX_RANGES) {
		count = FUZZ_MAX_RANGES;
	}
	size_t failed_pages;
	if (reference_compare(
		    data + 2, count, key_size, budget, &failed_pages
	    )) {
		abort();
	}
	return 0;
}
