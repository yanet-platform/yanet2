// Enumerations the generator turns into typed constants and a view whose
// enumeration has a 64-bit unsigned value with its top bit set.

#include <stdint.h>

enum fixture_u64 {
	fixture_magic = UINT64_C(0xDEAD10CC0000000F),
};

enum fixture_u32 {
	fixture_u32_ABI_version = 52,
};

enum fixture_flags {
	FIXTURE_FLAG_TOP = UINT64_C(0x8000000000000000),
};

enum fixture_signed {
	fixture_signed_low = -1,
	fixture_signed_high = 1,
};

struct fixture_flagged {
	enum fixture_flags flags;
};

int
main(void) {
	return 0;
}
