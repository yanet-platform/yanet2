#include "errors.h"

#include <errno.h>
#include <numaif.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

int
allocate_pages_on_numa(
	void *start,
	size_t size,
	uint16_t numa_idx,
	size_t page_size,
	yanet_error **err
) {
	if (numa_idx >= sizeof(uint64_t) * 8) {
		yanet_error_add(
			err,
			"numa index %u exceeds maximum %zu",
			numa_idx,
			sizeof(uint64_t) * 8 - 1
		);
		return -1;
	}
	uint64_t numa_mask = 1ull << numa_idx;

	if ((uintptr_t)start % page_size != 0) {
		yanet_error_add(
			err, "start address must be divisible by page size"
		);
		return -1;
	}
	if (size % page_size != 0) {
		yanet_error_add(err, "size must be divisible by page_size");
		return -1;
	}

	int orig_mode = MPOL_DEFAULT;
	uint64_t orig_mask = 0;
	if (get_mempolicy(
		    &orig_mode, &orig_mask, sizeof(orig_mask) * 8, NULL, 0
	    ) != 0) {
		yanet_error_add(
			err,
			"failed to read current memory policy: %s",
			strerror(errno)
		);
		return -1;
	}

	if (set_mempolicy(
		    MPOL_BIND | MPOL_F_STATIC_NODES,
		    &numa_mask,
		    sizeof(numa_mask) * 8
	    ) != 0) {
		yanet_error_add(
			err,
			"failed to bind memory to numa node: %s",
			strerror(errno)
		);
		return -1;
	}

	// TODO: intercept SIGBUS
	for (size_t i = 0; i < size; i += page_size) {
		((char *)start)[i] = 0;
	}

	if (set_mempolicy(orig_mode, &orig_mask, sizeof(orig_mask) * 8) != 0) {
		yanet_error_add(
			err,
			"failed to restore memory policy: %s",
			strerror(errno)
		);
		return -1;
	}

	return 0;
}