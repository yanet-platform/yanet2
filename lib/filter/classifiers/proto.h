#pragma once

#include "common/value.h"

struct proto_classifier {
	struct value_table tcp_flags;
	uint32_t max_tcp_class;
};