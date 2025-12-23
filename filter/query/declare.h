#pragma once

#include "common/for_each.h"

#define FILTER_ATTR_QUERY(name) name##_attr_query
#define FILTER_ATTR_QUERY_FUNC(name) name##_attr_query_func

#define FILTER1_ATTR_QUERY(name)                                               \
	(struct filter_attr_query) {                                           \
		FILTER_ATTR_QUERY_FUNC(name)                                   \
	}

#define FILTER_QUERY_DECLARE(tag, ...)                                         \
	static const struct filter_attr_query __filter_attrs_query_##tag[] = { \
		FOR_EACH(FILTER1_ATTR_QUERY, __VA_ARGS__)                      \
	};
