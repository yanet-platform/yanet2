// One definition of a structure another unit of the binary defines
// differently.

#include <stdint.h>

struct fixture_shared {
	uint32_t first;
	uint32_t second;
};

typedef int fixture_word;

struct fixture_aliased {
	fixture_word value;
};

typedef char fixture_char;

struct fixture_character {
	fixture_char value;
};

int
main(void) {
	return 0;
}
