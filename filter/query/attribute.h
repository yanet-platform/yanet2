#pragma once

#include "declare.h"
#include "device.h"
#include "net4.h"
#include "net6.h"
#include "port.h"
#include "proto.h"
#include "proto_range.h"
#include "vlan.h"

typedef uint32_t (*filter_attr_query_func)(struct packet *packet, void *data);

struct filter_attr_query {
	filter_attr_query_func query;
};

#define REGISTER_ATTRIBUTE(name)                                               \
	static inline uint32_t FILTER_ATTR_QUERY_FUNC(name)(                   \
		struct packet * packet, void *data                             \
	);                                                                     \
	static const struct filter_attr_query FILTER_ATTR_QUERY(name           \
	) = {FILTER_ATTR_QUERY_FUNC(name)}

REGISTER_ATTRIBUTE(port_src);
REGISTER_ATTRIBUTE(port_dst);
REGISTER_ATTRIBUTE(proto);
REGISTER_ATTRIBUTE(proto_range);
REGISTER_ATTRIBUTE(net4_src);
REGISTER_ATTRIBUTE(net4_dst);
REGISTER_ATTRIBUTE(net6_src);
REGISTER_ATTRIBUTE(net6_dst);
REGISTER_ATTRIBUTE(vlan);
REGISTER_ATTRIBUTE(device);

#undef REGISTER_ATTRIBUTE