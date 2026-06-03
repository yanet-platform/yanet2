#pragma once

#include "common/for_each.h"

#define FILTER_ATTR_COMPILER(name) name##_attr_compiler

#define FILTER_ATTR_COMPILER_INIT_FUNC(name) name##_attr_init
#define FILTER_ATTR_COMPILER_FREE_FUNC(name) name##_attr_free

#define FILTER_ATTR_LOOKUP_HANDLER(name)                                       \
	{                                                                      \
		name##_attr_init,                                              \
		name##_attr_free,                                              \
	}

#define FILTER_COMPILER_DECLARE(tag, ...)                                      \
	static const struct filter_compiler *tag = &(struct filter_compiler){  \
		sizeof((struct filter_lookup_handler[]                         \
		){FOR_EACH(FILTER_ATTR_LOOKUP_HANDLER, __VA_ARGS__)}) /        \
			sizeof(struct filter_lookup_handler),                  \
		(struct filter_lookup_handler[]                                \
		){FOR_EACH(FILTER_ATTR_LOOKUP_HANDLER, __VA_ARGS__)},          \
	};
