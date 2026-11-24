// Storage layouts the bootstrap accepts.
//
// Every reader of a storage assumes the placement the bootstrap gives it:
// the dataplane zone at the start, the control-plane zone dp_memory bytes
// in, both whole and aligned for their structures. The check runs alone
// here, without building a storage, so a layout it wrongly accepts fails
// the test instead of underflowing an arena.

#include <stdbool.h>
#include <stdint.h>

#include "common/test_assert.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/bootstrap.h"
#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"

// The check accepts a storage whose zones fit whole and aligned, and
// refuses each way of breaking the placement.
static int
run_storage_layout_valid_test(void) {
	_Alignas(64) static uint8_t storage[64];
	const size_t dp_memory = 4 << 20;
	const size_t cp_memory = 4 << 20;

	struct {
		const char *name;
		const void *storage;
		size_t dp_memory;
		size_t cp_memory;
		bool valid;
	} cases[] = {
		{"both zones whole and aligned",
		 storage,
		 dp_memory,
		 cp_memory,
		 true},
		{"zones exactly as large as their structures",
		 storage,
		 sizeof(struct dp_config),
		 sizeof(struct cp_config),
		 true},
		{"storage misaligned for the dataplane zone",
		 storage + 1,
		 dp_memory,
		 cp_memory,
		 false},
		{"dataplane memory smaller than the dataplane zone",
		 storage,
		 sizeof(struct dp_config) - 8,
		 cp_memory,
		 false},
		{"dataplane memory misaligning the control-plane zone",
		 storage,
		 dp_memory + 1,
		 cp_memory,
		 false},
		{"control-plane memory smaller than the control-plane zone",
		 storage,
		 dp_memory,
		 sizeof(struct cp_config) - 8,
		 false},
	};
	for (size_t idx = 0; idx < sizeof(cases) / sizeof(cases[0]); ++idx) {
		TEST_ASSERT_EQUAL(
			dp_storage_layout_valid(
				cases[idx].storage,
				cases[idx].dp_memory,
				cases[idx].cp_memory
			),
			cases[idx].valid,
			"%s",
			cases[idx].name
		);
	}
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");
	if (run_storage_layout_valid_test() != TEST_SUCCESS) {
		LOG(ERROR, "test storage_layout_valid failed");
		return 1;
	}
	LOG(INFO, "test storage_layout_valid passed");
	return 0;
}
