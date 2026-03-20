#pragma once

#include "common/likely.h"
#include "common/numutils.h"
#include "counters.h"
#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

/*
 * The function return bucket index of exponential histogram counter
 */
static inline uint64_t
counter_hist_bucket_exp2(
	uint64_t value, uint64_t min_bucket, uint64_t max_bucket
) {
	uint64_t bucket = uint64_log_up(value);
	if (bucket < min_bucket)
		bucket = min_bucket;
	bucket -= min_bucket;
	if (bucket > max_bucket - 1)
		bucket = max_bucket - 1;

	return bucket;
}

/**
 * Increment an exponential histogram counter by a specified value.
 *
 * This function increments a counter in an exponential histogram based on the
 * provided key. The key is mapped to a bucket using exponential (log2) scaling,
 * and the corresponding counter is incremented by the specified value.
 *
 * @param counter_id The ID of the counter in the counter registry
 * @param instance_id The instance ID for multi-instance counters
 * @param counter_storage Pointer to the counter storage structure
 * @param min_bucket The minimum bucket index (log2 scale)
 * @param max_bucket The maximum bucket index (log2 scale)
 * @param key The value to determine which bucket to increment
 * @param value The amount to increment the counter by
 */
static inline void
counter_hist_exp2_inc(
	uint64_t counter_id,
	uint64_t instance_id,
	struct counter_storage *counter_storage,
	uint64_t min_bucket,
	uint64_t max_bucket,
	uint64_t key,
	uint64_t value
) {
	uint64_t bucket = counter_hist_bucket_exp2(key, min_bucket, max_bucket);

	counter_get_address(counter_id, instance_id, counter_storage)[bucket] +=
		value;
}

////////////////////////////////////////////////////////////////////////////////

/**
 * Configuration structure for a hybrid histogram counter.
 *
 * A hybrid histogram combines linear and exponential bucket distributions to
 * provide fine-grained resolution for small values (linear buckets) and
 * efficient coverage of large value ranges (exponential buckets).
 *
 * The histogram layout is:
 * - Bucket 0: Values below min_value (underflow bucket)
 * - Buckets 1 to linear_hists: Linear buckets with fixed step size
 * - Remaining buckets: Exponential buckets with logarithmic scaling
 * - Last bucket: Overflow bucket for values exceeding the range
 */
struct counters_hybrid_histogram {
	uint64_t min_value;    ///< Minimum value for the first linear bucket
	uint64_t linear_step;  ///< Step size for linear buckets
	uint64_t linear_hists; ///< Number of linear buckets
	uint64_t exp_hists;    ///< Number of exponential buckets
};

/**
 * Calculate the bucket index for a value in a hybrid histogram.
 *
 * This function determines which bucket a given value belongs to in a hybrid
 * histogram. Values below min_value go to bucket 0 (underflow). Values within
 * the linear range are distributed across linear buckets with fixed step size.
 * Values beyond the linear range are distributed across exponential buckets
 * with logarithmic scaling.
 *
 * @param hist Pointer to the hybrid histogram configuration
 * @param value The value to map to a bucket
 * @return The bucket index for the given value
 */
static inline size_t
counters_hybrid_histogram_batch(
	const struct counters_hybrid_histogram *hist, uint64_t value
) {
	if (unlikely(value < hist->min_value)) {
		return 0;
	}

	const uint64_t linear_boundary =
		hist->min_value + hist->linear_step * hist->linear_hists;

	const uint64_t linear = (value - hist->min_value) / hist->linear_step;
	const uint64_t exp_part =
		uint64_log_down(2 * (value / linear_boundary));

	const uint64_t idx =
		1 +
		(linear < hist->linear_hists ? linear : hist->linear_hists - 1
		) +
		exp_part;
	const uint64_t total_hists = hist->linear_hists + hist->exp_hists + 2;

	return idx < total_hists ? idx : total_hists - 1;
}

/**
 * Calculate the total number of buckets in a hybrid histogram.
 *
 * Returns the total count of histogram buckets, which includes:
 * - 1 underflow bucket (for values below min_value)
 * - linear_hists linear buckets (with fixed step size)
 * - exp_hists exponential buckets (with logarithmic scaling)
 * - 1 overflow bucket (for values exceeding the range)
 *
 * Total buckets = 2 + linear_hists + exp_hists
 *
 * This function is used to determine the size of counter arrays needed to
 * store histogram data and to iterate over all buckets when processing
 * performance metrics.
 *
 * @param hist Pointer to the hybrid histogram configuration
 * @return Total number of histogram buckets
 */
size_t
counters_hybrid_histogram_batches(const struct counters_hybrid_histogram *hist);

/**
 * Get the minimum value (lower bound) for a specific histogram bucket.
 *
 * Returns the minimum value in nanoseconds that would be placed into the
 * specified bucket. This is used to label histogram buckets when reporting
 * performance metrics.
 *
 * Bucket layout:
 * - Bucket 0: Underflow (returns 0)
 * - Buckets 1 to linear_hists: Linear buckets (min_value + step * index)
 * - Remaining buckets: Exponential buckets (logarithmic scaling)
 * - Last bucket: Overflow (returns maximum representable value)
 *
 * @param hist Pointer to the hybrid histogram configuration
 * @param batch Bucket index (0 to counters_hybrid_histogram_batches() - 1)
 * @return Minimum value in nanoseconds for the specified bucket
 */
uint64_t
counters_hybrid_histogram_batch_first_elem(
	const struct counters_hybrid_histogram *hist, uint64_t batch
);