// This unit uses unsigned enum fields and forward-declares two enums.

#include <stdint.h>

enum fixture_sign : uint32_t {
	fixture_sign_one = 1,
};

struct fixture_sign_holder {
	enum fixture_sign value;
};

typedef enum : uint32_t { fixture_alias_one = 1 } fixture_alias;

struct fixture_alias_holder {
	fixture_alias value;
};

struct fixture_alias_array {
	fixture_alias values[2];
};

struct fixture_envelope {
	struct fixture_alias_array inner;
};

struct fixture_anonymous_holder {
	struct {
		fixture_alias values[2];
	} inner;
};

typedef const fixture_alias *fixture_alias_pointer;

struct fixture_alias_indirect {
	fixture_alias_pointer value;
};

enum fixture_width : uint8_t;

struct fixture_width_holder {
	enum fixture_width value;
};

enum fixture_forward : int32_t;
typedef enum fixture_forward fixture_forward_value;

struct fixture_forward_holder {
	enum fixture_forward value;
	const enum fixture_forward qualified;
	fixture_forward_value aliased;
};

int
main(void) {
	return 0;
}
