#pragma once

#include "api/inspect.h"
#include "handler.h"

////////////////////////////////////////////////////////////////////////////////

// TODO: docs
void
packet_handler_inspect(
	struct packet_handler *handler,
	struct packet_handler_inspect *inspect,
	size_t workers
);