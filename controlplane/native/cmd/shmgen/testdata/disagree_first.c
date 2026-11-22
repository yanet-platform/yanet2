// One definition of a structure another unit of the binary defines
// differently.

#include <stdint.h>

struct fixture_shared {
	uint32_t first;
	uint32_t second;
};

int
main(void) {
	return 0;
}
