// The signed definitions in this unit exercise both compatible and
// conflicting declarations across translation units.

#include <stdint.h>

enum fixture_sign : int32_t {
	fixture_sign_one = 1,
};

typedef enum : int32_t { fixture_alias_one = 1 } fixture_alias;

struct fixture_alias_holder {
	fixture_alias value;
};

struct fixture_alias_array {
	fixture_alias values[2];
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

enum fixture_width : int64_t { fixture_width_one = 1 };

enum fixture_forward : int32_t { fixture_forward_one = 1 };
typedef enum fixture_forward fixture_intermediate;
typedef fixture_intermediate fixture_forward_value;

struct fixture_forward_holder {
	enum fixture_forward value;
	const enum fixture_forward qualified;
	fixture_forward_value aliased;
};
