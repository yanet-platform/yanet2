#pragma once

#include "declare.h"
#include "device.h"
#include "net4.h"
#include "net4_fast.h"
#include "net6.h"
#include "net6_fast.h"
#include "port.h"
#include "port_fast.h"
#include "proto.h"
#include "proto_range.h"
#include "proto_range_fast.h"
#include "vlan.h"

typedef void (*filter_attr_query_func)(
	void *data, struct packet **packets, uint32_t *result, uint32_t idx
);

struct filter_attr_query {
	filter_attr_query_func query;
};

#define REGISTER_ATTRIBUTE(name)                                               \
	static inline void FILTER_ATTR_QUERY_FUNC(name)(                       \
		void *data,                                                    \
		struct packet **packets,                                       \
		uint32_t *result,                                              \
		uint32_t count                                                 \
	);

REGISTER_ATTRIBUTE(port_src);
REGISTER_ATTRIBUTE(port_dst);
REGISTER_ATTRIBUTE(proto);
REGISTER_ATTRIBUTE(port_fast_src);
REGISTER_ATTRIBUTE(port_fast_dst);
REGISTER_ATTRIBUTE(proto_range);
REGISTER_ATTRIBUTE(proto_range_fast);
REGISTER_ATTRIBUTE(net4_src);
REGISTER_ATTRIBUTE(net4_dst);
REGISTER_ATTRIBUTE(net6_src);
REGISTER_ATTRIBUTE(net6_dst);
REGISTER_ATTRIBUTE(vlan);
REGISTER_ATTRIBUTE(device);
REGISTER_ATTRIBUTE(net4_fast_dst);
REGISTER_ATTRIBUTE(net4_fast_src);
REGISTER_ATTRIBUTE(net6_fast_dst);
REGISTER_ATTRIBUTE(net6_fast_src);

#undef REGISTER_ATTRIBUTE
