#pragma once

#include "common/for_each.h"

#define FILTER_ATTR_COMPILER(name) name##_attr_compiler

#define FILTER_ATTR_COMPILER_INIT_FUNC(name) name##_attr_init
#define FILTER_ATTR_COMPILER_FREE_FUNC(name) name##_attr_free

#define __FILTER_ATTR_COMP(name)                                               \
	(struct filter_attr_compiler) {                                        \
		FILTER_ATTR_COMPILER_INIT_FUNC(name),                          \
			FILTER_ATTR_COMPILER_FREE_FUNC(name)                   \
	}

#define FILTER_COMPILER_DECLARE(tag, ...)                                      \
	static const struct filter_attr_compiler                               \
		__filter_attrs_compiler_##tag[] = {                            \
			FOR_EACH(__FILTER_ATTR_COMP, __VA_ARGS__)              \
	};
