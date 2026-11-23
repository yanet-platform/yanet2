// Declarations whose Go names collide.

#include <stdint.h>

// A field and the accessor of a flexible array member with one Go name.
struct fixture_accessor {
	uint64_t items_;
	uint64_t items[];
};

// An enumerator constant with the name of a view.
enum fixture_kind {
	FIXTURE_NODE = 1,
};

struct fixture_node {
	uint64_t value;
};

struct fixture_named {
	enum fixture_kind kind;
	struct fixture_node node;
};

// Two enumerators with one Go name.
enum fixture_twins {
	FIXTURE_TWIN = 1,
	fixture_twin = 2,
};

struct fixture_twinned {
	enum fixture_twins twins;
};

enum fixture_tag : unsigned;
enum fixture__tag : unsigned { fixture_tag_one = 1 };

struct fixture_tag_collision {
	enum fixture_tag partial;
	enum fixture__tag defined;
};

int
main(void) {
	return 0;
}
