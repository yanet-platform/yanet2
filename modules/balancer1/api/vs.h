#pragma once

#include <stddef.h>
#include <stdint.h>

#define BALANCER_VS_PURE_L3_FLAG ((uint64_t)(1ull << 0))
#define BALANCER_VS_IPV6_FLAG ((uint64_t)(1ull << 1))

#define BALANCER_VS_FIX_MSS_FLAG ((uint64_t)(1ull << 2))
#define BALANCER_VS_GRE_FLAG ((uint64_t)(1ull << 3))

#define BALANCER_VS_OPS_FLAG ((uint64_t)(1ull << 4))

#define BALANCER_REAL_IPV6_FLAG ((uint64_t)(1ull << 0))

struct agent;
struct balancer_vs_config;

// Create new virtual service config
struct balancer_vs_config *
balancer_vs_config_create(
	struct agent *agent,
	uint64_t flags,
	uint8_t *ip,
	uint16_t port,
	uint8_t proto,
	size_t real_count,
	size_t allowed_src_count
);

// Allows to free virtual service config
void
balancer_vs_config_free(
	struct balancer_vs_config *vs_config
);


// Allows to setup one real of the virtual service
void
balancer_vs_config_set_real(
	struct balancer_vs_config *vs_config,
	size_t index,
	uint64_t flags,
	uint16_t weight,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
);

// Allows to set one source address of the incoming packet, allowed by virtual
// service
void
balancer_vs_config_set_allowed_src_range(
	struct balancer_vs_config *vs_config,
	size_t index,
	uint8_t *from,
	uint8_t *to
);