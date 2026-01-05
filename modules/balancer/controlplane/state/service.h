#pragma once

#include "real.h"
#include "vs.h"

#include <stdbool.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

union service_state {
	struct real_state real;
	struct vs_state vs;
};

union service_identifier {
	struct real_identifier real;
	struct vs_identifier vs;
};

static inline union service_identifier *
service_id(union service_state *service) {
	return (union service_identifier *)service;
}
