// A structure the generator turns into a view.

#include <stdint.h>

struct fixture_view {
	uint32_t count;
	uint64_t total;
	struct fixture_view *next;
};

struct fixture_bits {
	uint8_t lead;
	unsigned first : 3;
	unsigned second : 10;
	unsigned last : 1;
	uint8_t tail;
};

struct fixture_high_bits {
	unsigned : 30;
	unsigned high : 2;
};

int
main(void) {
	return 0;
}
