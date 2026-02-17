#include "common/rng.h"
#include <assert.h>
#include <common/test_assert.h>
#include <lib/counters/histogram.h>
#include <lib/logging/log.h>

////////////////////////////////////////////////////////////////////////////////

int
test_basic() {
	struct counters_hybrid_histogram hist = {
		.min_value = 10,
		.linear_step = 50,
		.linear_hists = 20,
		.exp_hists = 9
	};
	const uint64_t first_exp = 10 + 50 * 20;
	struct test_case {
		uint64_t value;
		uint64_t expected;
	} cases[] = {
		{0, 0},
		{9, 0},
		{10, 1},
		{11, 1},
		{59, 1},
		{60, 2},
		{61, 2},
		{109, 2},
		{110, 3},
		{first_exp - 1, 20},
		{first_exp, 21},
		{2 * first_exp - 1, 21},
		{2 * first_exp, 22},
		{2 * first_exp + 1, 22},
		{4 * first_exp - 1, 22},
		{4 * first_exp, 23},
		{(1 << 9) * first_exp - 1, 29},
		{(1 << 9) * first_exp, 30},
		{(1 << 10) * first_exp - 1, 30},
		{(1 << 10) * first_exp, 30},
		{(1 << 30), 30}
	};
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
		uint64_t expected = cases[i].expected;
		uint64_t batch =
			counters_hybrid_histogram_batch(&hist, cases[i].value);
		TEST_ASSERT_EQUAL(
			batch,
			expected,
			"got invalid batch for test_case at index %zu",
			i
		);
	}

	TEST_ASSERT_EQUAL(
		counters_hybrid_histogram_batches(&hist),
		hist.linear_hists + hist.exp_hists + 2,
		"got invalid number of batches"
	);

	return TEST_SUCCESS;
}

int
stress_test(
	struct counters_hybrid_histogram *hist, size_t queries, uint64_t rng
) {
	const size_t num_batches = counters_hybrid_histogram_batches(hist);
	TEST_ASSERT_EQUAL(
		num_batches,
		hist->linear_hists + hist->exp_hists + 2,
		"got invalid number of batches"
	);

	struct segment {
		uint64_t from;
		uint64_t to; // non-inclusive
	};
	struct segment *segments = malloc(sizeof(struct segment) * num_batches);
	segments[0] = (struct segment){0, hist->min_value};
	for (size_t i = 1; i <= hist->linear_hists; i++) {
		segments[i] = (struct segment
		){hist->min_value + (i - 1) * hist->linear_step,
		  hist->min_value + i * hist->linear_step};
	}
	for (size_t i = hist->linear_hists + 1; i < num_batches; ++i) {
		segments[i].from = segments[i - 1].to;
		segments[i].to = segments[i].from * 2;
	}

	const uint64_t boundary = segments[num_batches - 1].to;

	for (size_t query = 0; query < queries; ++query) {
		rng = rng_next(&rng) % boundary;
		const uint64_t value = rng;
		uint64_t batch_got =
			counters_hybrid_histogram_batch(hist, value);
		uint64_t found = (uint64_t)-1;
		for (size_t i = 0;
		     i <= hist->linear_hists + hist->exp_hists + 1;
		     i++) {
			if (segments[i].from <= value &&
			    value < segments[i].to) {
				found = i;
				break;
			}
		}
		assert(found != (uint64_t)-1);
		TEST_ASSERT_EQUAL(
			batch_got,
			found,
			"got invalid batch for query at index %zu (value=%lu)",
			query,
			value
		);
	}

	for (size_t i = 0; i < num_batches; ++i) {
		uint64_t first_elem =
			counters_hybrid_histogram_batch_first_elem(hist, i);
		TEST_ASSERT_EQUAL(
			first_elem,
			segments[i].from,
			"got invalid first element for batch %zu",
			i
		);
	}

	free(segments);

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	size_t tests_count = 0;
	size_t tests_failed = 0;

	++tests_count;
	if (test_basic() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_basic failed");
	}

	uint64_t min_values[] = {10, 20, 40, 100, 1000, 0};
	uint64_t steps[] = {10, 20, 32, 16, 100, 1000};
	size_t linear_hist_counts[] = {1, 2, 4, 16, 20};
	size_t exp_hist_counts[] = {1, 2, 8, 12};

	const size_t queries = 1000;

	uint64_t rng = 123;

	for (size_t min_value_idx = 0;
	     min_value_idx < sizeof(min_values) / sizeof(min_values[0]);
	     ++min_value_idx) {
		for (size_t step_idx = 0;
		     step_idx < sizeof(steps) / sizeof(steps[0]);
		     ++step_idx) {
			for (size_t linear_hist_count_idx = 0;
			     linear_hist_count_idx <
			     sizeof(linear_hist_counts) /
				     sizeof(linear_hist_counts[0]);
			     ++linear_hist_count_idx) {
				for (size_t exp_hist_count_idx = 0;
				     exp_hist_count_idx <
				     sizeof(exp_hist_counts) /
					     sizeof(exp_hist_counts[0]);
				     ++exp_hist_count_idx) {
					++tests_count;
					struct counters_hybrid_histogram hist = {
						.min_value = min_values
							[min_value_idx],
						.linear_step = steps[step_idx],
						.linear_hists = linear_hist_counts
							[linear_hist_count_idx],
						.exp_hists = exp_hist_counts
							[exp_hist_count_idx]
					};

					LOG(INFO,
					    "stress test, hist=[min_value=%lu, "
					    "linear_step=%lu, "
					    "linear_hists=%zu, exp_hists=%zu]",
					    hist.min_value,
					    hist.linear_step,
					    hist.linear_hists,
					    hist.exp_hists);

					if (stress_test(
						    &hist, queries, ++rng
					    ) != TEST_SUCCESS) {
						++tests_failed;
						LOG(ERROR,
						    "test_stress failed");
					}
				}
			}
		}
	}

	if (tests_failed > 0) {
		LOG(ERROR, "tests failed: %zu/%zu", tests_failed, tests_count);
		return 1;
	} else {
		LOG(INFO, "All %zu tests passed", tests_count);
		return 0;
	}
}