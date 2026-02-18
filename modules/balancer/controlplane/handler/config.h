#pragma once

#include "api/handler.h"
#include "common/memory.h"

// TODO: docs
int
packet_handler_config_from_relative(
	struct packet_handler_config *dst, struct packet_handler_config *src
);

// TODO: docs
int
packet_handler_config_to_relative(
	struct packet_handler_config *dst,
	struct packet_handler_config *src,
	struct memory_context *mctx
);