// Pointers the C declarations do not tell apart, and a flexible array
// member.

#include <stdint.h>

struct fixture_target {
	uint64_t value;
};

struct fixture_pointers {
	// A pointer the dataplane resolves for its own process.
	struct fixture_target *abs_target;
	// A process-local pointer named on the command line.
	struct fixture_target *resolved;
	// A relative pointer to an array of absolute ones.
	struct fixture_target **abs_relative_targets;
	uint64_t count;
	struct fixture_target items[];
};

struct fixture_table {
	// A relative pointer to absolute ones, without the abs_ prefix.
	struct fixture_target **targets;
	uint64_t count;
	// Absolute pointers only the dataplane fills.
	struct fixture_target *slots[];
};

int
main(void) {
	return 0;
}
