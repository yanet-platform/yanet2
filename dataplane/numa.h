#pragma once

#include "errors.h"
#include <stddef.h>
#include <stdint.h>

// Allocates the pages of [start, start+size) on NUMA node numa_idx.
//
// Pages of a shared mapping follow the memory policy of the thread that
// first touches them. So this binds the calling thread to numa_idx and
// touches every page to fault it in there. Pre-faulting here also pins the
// pages before another process (e.g. the controlplane) can touch them first.
//
// The pages must not be touched previously: an already-faulted page keeps
// its current node, as first-touch no longer applies to it.
//
// The bind is strict, so the touch raises SIGBUS when numa_idx has no free
// (huge)pages left.
//
// The start and size must be divisible by page_size.
int
allocate_pages_on_numa(
	void *start,
	size_t size,
	uint16_t numa_idx,
	size_t page_size,
	yanet_error **err
);