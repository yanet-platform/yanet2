#include "common/numutils.h"

#include <assert.h>
#include <stdint.h>

static void
test_next_power_of_two_returns_one_for_small_values(void) {
	assert(next_power_of_two(0) == 1);
	assert(next_power_of_two(1) == 1);
}

static void
test_next_power_of_two_rounds_nonpowers_up(void) {
	assert(next_power_of_two(2) == 2);
	assert(next_power_of_two(3) == 4);
	assert(next_power_of_two((1ull << 62) + 1) == (1ull << 63));
}

static void
test_next_power_of_two_saturates_at_highest_supported_power(void) {
	assert(next_power_of_two(1ull << 63) == (1ull << 63));
	assert(next_power_of_two((1ull << 63) + 1) == (1ull << 63));
	assert(next_power_of_two(UINT64_MAX) == (1ull << 63));
}

int
main(void) {
	test_next_power_of_two_returns_one_for_small_values();
	test_next_power_of_two_rounds_nonpowers_up();
	test_next_power_of_two_saturates_at_highest_supported_power();
	return 0;
}
