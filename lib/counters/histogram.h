#pragma once

#include "common/likely.h"
#include "common/numutils.h"
#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

// TODO: docs
struct counters_hybrid_histogram {
	uint64_t min_value;

	uint64_t linear_step;

	uint64_t linear_hists;

	uint64_t exp_hists;
};

// TODO: docs
static inline size_t
counters_hybrid_histogram_batch(
	const struct counters_hybrid_histogram *hist, uint64_t value
) {
	if (unlikely(value < hist->min_value)) {
		return 0;
	}

	uint64_t max_linear_value =
		hist->min_value + hist->linear_step * hist->linear_hists;

	uint64_t max_value =
		hist->min_value + hist->linear_step * hist->linear_hists *
					  (1ull << hist->exp_hists);
	if (unlikely(value > max_value)) {
		return hist->linear_hists + hist->exp_hists - 1;
	}

	// help compiler to optimize branching using cmov
	uint64_t linear_exp_idx =
		hist->linear_hists + uint64_log(value - max_linear_value);
	uint64_t linear_idx = (value - hist->min_value) / hist->linear_step;
	return value <= max_linear_value ? linear_idx : linear_exp_idx;
}