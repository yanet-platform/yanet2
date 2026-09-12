#pragma once

#include <stdint.h>

#include "lib/filter2/filter.h"

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

/*
 * Instantiates the handler table of a module authored attribute
 * lookup.
 *
 * The library itself carries no packet knowledge: how a packet yields
 * an attribute value — the header parsing and the region walk — is
 * encoded by the consumer, as a static inline routine whose getter
 * calls are direct and therefore inlined into the lookup body. The
 * only indirect call left is the per batch lookup dispatch.
 *
 * A query signature is the module's own array of these instances,
 * written in the order of its FILTER_COMPILER_DECLARE signature:
 *
 *	static const struct filter_query_attr_handlers *my_query[] = {
 *		&my_attr_device,
 *		&my_attr_vlan,
 *	};
 *	filter_query(filter, my_query, packets, results, count);
 */
#define FILTER_QUERY_ATTR(tag, fn)                                             \
	static const struct filter_query_attr_handlers tag = {                 \
		.lookup = fn,                                                  \
	};
