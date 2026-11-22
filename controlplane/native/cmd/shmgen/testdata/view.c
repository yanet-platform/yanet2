// A structure the generator turns into a view.

#include <stdint.h>

struct fixture_view {
	uint32_t count;
	uint64_t total;
	struct fixture_view *next;
};

int
main(void) {
	return 0;
}
