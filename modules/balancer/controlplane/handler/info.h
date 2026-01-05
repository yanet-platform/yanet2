#pragma once

#include "api/session.h"
#include <stddef.h>

struct packet_handler;
struct balancer_info;

size_t
packet_handler_sessions_info(
	struct packet_handler *handler,
	struct named_session_info **sessions,
	uint32_t now
);

void
packet_handler_balancer_info(
	struct packet_handler *handler, struct balancer_info *info, uint32_t now
);
