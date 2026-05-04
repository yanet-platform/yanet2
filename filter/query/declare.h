#pragma once

#include <stdint.h>

#include "filter/filter.h"

#include "common/for_each.h"

#define FILTER_ATTR_QUERY(name) &filter_query_attr_##name.attr_handlers

#define FILTER_QUERY_DECLARE(tag, ...)                                         \
	static const struct filter_query_attr_handlers *tag[] = {              \
		FOR_EACH(FILTER_ATTR_QUERY, __VA_ARGS__),                      \
	};

struct packet;

struct filter_query_attr_handlers;

typedef void (*filter_query_attr_lookup_func)(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *result,
	uint32_t packet_count
);

struct filter_query_attr_handlers {
	filter_query_attr_lookup_func lookup;
};
