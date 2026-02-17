#include "histogram.h"

size_t
counters_hybrid_histogram_batches(const struct counters_hybrid_histogram *hist
) {
	return hist->linear_hists + hist->exp_hists + 2;
}

uint64_t
counters_hybrid_histogram_batch_first_elem(
	const struct counters_hybrid_histogram *hist, uint64_t batch
) {
	if (batch == 0) {
		return 0;
	}
	if (batch <= hist->linear_hists) {
		return hist->min_value + (batch - 1) * hist->linear_step;
	}
	return (hist->min_value + hist->linear_hists * hist->linear_step) *
	       (1ull << (batch - hist->linear_hists - 1));
}