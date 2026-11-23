// Packed layouts with and without room for Go's trailing padding.

#include <stdint.h>

struct __attribute__((packed)) fixture_short_tail {
	uint64_t value;
	uint8_t tag;
};

struct fixture_tail_array {
	uint64_t count;
	struct fixture_short_tail entries[2];
};

struct fixture_empty {};

struct fixture_empty_tail {
	uint64_t value;
	struct fixture_empty end;
};

typedef uint64_t fixture_values[];

struct fixture_header {
	uint8_t count;
	fixture_values values;
};

struct __attribute__((packed)) fixture_packed {
	double _Complex sample;
	uint8_t tag;
	uint32_t word;
	struct fixture_header header;
};

int
main(void) {
	return 0;
}
