#pragma once

#include <stdint.h>

#include "lib/classify/classify.h"

#include "common/for_each.h"

#define CLASSIFY_ATTR_QUERY(name) &classify_query_attr_##name.attr_handlers

#define CLASSIFY_QUERY_DECLARE(tag, ...)                                       \
	static const struct classify_query_attr_handlers *tag[] = {            \
		FOR_EACH(CLASSIFY_ATTR_QUERY, __VA_ARGS__),                    \
	};

struct packet;

struct classify_query_attr_handlers;

typedef void (*classify_query_attr_lookup_func)(
	const struct classify_query_attr *attr,
	const struct classify_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *result,
	uint32_t packet_count
);

struct classify_query_attr_handlers {
	classify_query_attr_lookup_func lookup;
};
