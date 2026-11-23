// Scalars whose Go type depends on more than their size.

struct fixture_scalars {
	signed char explicitly_signed;
	char plain;
	unsigned char explicitly_unsigned;
	enum {
		fixture_anonymous_low = 1,
		fixture_anonymous_high = 2
	} anonymous;
	__int128 wide;
	long double extended;
	_Float16 half;
};

int
main(void) {
	return 0;
}
