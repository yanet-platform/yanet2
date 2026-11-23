// The other definition of a structure the first unit defines with
// another layout.

#include <stdint.h>

struct fixture_shared {
	uint64_t first;
};

typedef unsigned int fixture_word;

struct fixture_aliased {
	fixture_word value;
};

typedef signed char fixture_char;

struct fixture_character {
	fixture_char value;
};
