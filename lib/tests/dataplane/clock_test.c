#include <stddef.h>
#include <stdint.h>

#include "common/test_assert.h"

#include "lib/dataplane/time/clock.h"
#include "lib/logging/log.h"

// One synthetic TSC frequency to exercise the tick-to-nanosecond
// conversion at.
struct tsc_freq_case {
	const char *name;
	uint64_t hz;
};

static const struct tsc_freq_case freq_cases[] = {
	{"2.0GHz", 2000000000ULL},
	{"2.2GHz", 2200000000ULL},
	{"2.5GHz", 2500000000ULL},
	{"2.9GHz", 2900000000ULL},
	{"3.6GHz", 3600000000ULL},
	{"4.0GHz", 4000000000ULL},
};

#define FREQ_CASE_COUNT (sizeof(freq_cases) / sizeof(freq_cases[0]))

// Verifies that tsc_clock_mult_from_hz returns 0 for a zero frequency,
// so callers can detect an unusable TSC frequency instead of dividing
// by zero.
static int
test_mult_from_hz_zero(void) {
	TEST_ASSERT_EQUAL(tsc_clock_mult_from_hz(0), 0, "mult for hz=0");

	return TEST_SUCCESS;
}

// Verifies that converting exactly one second worth of ticks to
// nanoseconds stays within the multiplier's provable rounding bound,
// across a spread of realistic TSC frequencies including 2.5GHz -- the
// frequency at which the previous 1024-tick pre-shift scheme was off
// by 0.1465%.
static int
test_one_second_within_bound(void) {
	for (size_t idx = 0; idx < FREQ_CASE_COUNT; ++idx) {
		uint64_t hz = freq_cases[idx].hz;
		uint64_t mult = tsc_clock_mult_from_hz(hz);
		TEST_ASSERT(
			mult != 0, "mult is zero for %s", freq_cases[idx].name
		);

		uint64_t ns = tsc_clock_ticks_to_ns(hz, mult);
		uint64_t bound = hz / (1ULL << 32) + 1;
		uint64_t diff = ns > 1000000000ULL ? ns - 1000000000ULL
						   : 1000000000ULL - ns;
		TEST_ASSERT(
			diff <= bound,
			"one-second conversion out of bound for %s: "
			"ns=%lu diff=%lu bound=%lu",
			freq_cases[idx].name,
			(unsigned long)ns,
			(unsigned long)diff,
			(unsigned long)bound
		);
	}

	return TEST_SUCCESS;
}

// Verifies that at frequencies where the 32.32 multiplier divides
// evenly, a full second of ticks converts to exactly one billion
// nanoseconds with no rounding error at all.
static int
test_exact_frequencies(void) {
	static const uint64_t exact_hz[] = {2000000000ULL, 4000000000ULL};

	for (size_t idx = 0; idx < sizeof(exact_hz) / sizeof(exact_hz[0]);
	     ++idx) {
		uint64_t hz = exact_hz[idx];
		uint64_t mult = tsc_clock_mult_from_hz(hz);
		uint64_t ns = tsc_clock_ticks_to_ns(hz, mult);
		TEST_ASSERT_EQUAL(
			ns,
			1000000000ULL,
			"exact one-second conversion for hz=%lu",
			(unsigned long)hz
		);
	}

	return TEST_SUCCESS;
}

// Verifies that a nine-hour uptime -- the duration of the vla1-4fw15
// production incident -- converts within a millisecond of the true
// elapsed time. The previous 1024-tick pre-shift scheme missed this
// bound by about 47.5 seconds at 2.5GHz.
static int
test_nine_hour_uptime_within_bound(void) {
	const uint64_t seconds = 32400;
	const uint64_t expected_ns = seconds * 1000000000ULL;
	const uint64_t bound_ns = 1000000ULL;

	for (size_t idx = 0; idx < FREQ_CASE_COUNT; ++idx) {
		uint64_t hz = freq_cases[idx].hz;
		uint64_t mult = tsc_clock_mult_from_hz(hz);
		uint64_t ticks = hz * seconds;
		uint64_t ns = tsc_clock_ticks_to_ns(ticks, mult);
		uint64_t diff =
			ns > expected_ns ? ns - expected_ns : expected_ns - ns;
		TEST_ASSERT(
			diff <= bound_ns,
			"nine-hour conversion out of bound for %s: "
			"ns=%lu diff=%lu",
			freq_cases[idx].name,
			(unsigned long)ns,
			(unsigned long)diff
		);
	}

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting Clock Test Suite ===");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"mult_from_hz_zero", test_mult_from_hz_zero},
		{"one_second_within_bound", test_one_second_within_bound},
		{"exact_frequencies", test_exact_frequencies},
		{"nine_hour_uptime_within_bound",
		 test_nine_hour_uptime_within_bound},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t i = 0; i < total; i++) {
		LOG(INFO, "[%zu/%zu] running %s...", i + 1, total, tests[i].name
		);
		if (tests[i].fn() != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[i].name);
			failed++;
		} else {
			LOG(INFO, "%s passed", tests[i].name);
		}
	}

	if (failed == 0) {
		LOG(INFO, "=== All %zu clock tests passed! ===", total);
	} else {
		LOG(ERROR, "=== %zu/%zu clock tests failed ===", failed, total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
