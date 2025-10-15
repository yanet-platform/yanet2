#pragma once

#include "ring.h"

#include <common/lpm.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////
// Virtual Service Flags
////////////////////////////////////////////////////////////////////////////////

typedef uint8_t balancer_vs_flags_t;

////////////////////////////////////////////////////////////////////////////////

#define BALANCER_VS_PURE_L3_FLAG ((uint8_t)(1u << 0))
#define BALANCER_VS_IPV6_FLAG ((uint8_t)(1u << 3))

#define BALANCER_VS_FIX_MSS_FLAG ((uint8_t)(1u << 1))
#define BALANCER_VS_GRE_FLAG ((uint8_t)(1u << 4))

#define BALANCER_VS_OPS_FLAG ((uint8_t)(1u << 2))

////////////////////////////////////////////////////////////////////////////////

struct balancer_vs {
	balancer_vs_flags_t flags;

	uint8_t address[16];

	uint16_t port;
	uint8_t proto;

	uint64_t real_start;
	uint64_t real_count;

	struct lpm src_filter;

	struct ring real_ring;
};