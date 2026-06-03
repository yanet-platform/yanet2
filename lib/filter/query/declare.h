#pragma once

#include "common/for_each.h"

#define FILTER_ATTR_QUERY_FUNC(name) name##_attr_query_func

#define FILTER_QUERY_DECLARE(tag, ...)                                         \
	static const struct filter_query *tag = &(const struct filter_query){  \
		sizeof((filter_lookup_query_func[]                             \
		){FOR_EACH(FILTER_ATTR_QUERY_FUNC, __VA_ARGS__)}) /            \
			sizeof(filter_lookup_query_func),                      \
		(filter_lookup_query_func[]                                    \
		){FOR_EACH(FILTER_ATTR_QUERY_FUNC, __VA_ARGS__)},              \
	};
