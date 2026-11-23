// A structure reaching others through pointers, with only their
// declarations in this unit.

struct fixture_inner;
struct fixture_opaque;

struct fixture_outer {
	struct fixture_inner *inner;
	struct fixture_opaque *opaque;
};

int
main(void) {
	return 0;
}
