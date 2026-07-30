#pragma once

#include "declare.h"
#include "device.h"
#include "net4.h"
#include "net6.h"
#include "port.h"
#include "proto_range.h"
#include "vlan.h"

typedef void (*filter_attr_query_func)(
	void *data, struct packet **packets, uint32_t *result, uint32_t idx
);

struct filter_attr_query {
	filter_attr_query_func query;
};
