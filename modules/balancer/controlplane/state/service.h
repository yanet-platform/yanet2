#pragma once

#include "real.h"
#include "vs.h"

#include <stdbool.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

union service {
	struct real_state real;
	struct vs_state vs;
};

union service_identifier {
	struct real_identifier real;
	struct vs_identifier vs;
};

static inline union service_identifier *
service_id(union service *service) {
	return (union service_identifier *)service;
}

union service_info {
	struct sharded_real_info real;
	struct sharded_vs_info vs;
};

void
real_info_accum(struct real_info *dst, union service_info *src, size_t workers);

void
vs_info_accum(struct vs_info *dst, union service_info *src, size_t workers);
