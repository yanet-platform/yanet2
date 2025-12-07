#pragma once

#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

// Virtual service flags

/// If virtual service serves all ports.
/// Destination port of the packet will be saved.
#define BALANCER_VS_PURE_L3_FLAG ((uint64_t)(1ull << 0))

/// If virtual service serves IPv6 address.
#define BALANCER_VS_IPV6_FLAG ((uint64_t)(1ull << 1))

/// Fix MSS TCP option.
#define BALANCER_VS_FIX_MSS_FLAG ((uint64_t)(1ull << 2))

/// Use GRE tunneling protocol when transfering packet to real.
#define BALANCER_VS_GRE_FLAG ((uint64_t)(1ull << 3))

/// One Packet Scheduling option disables sessions with the virtual service.
/// Packets with the same source will be scheduled independently.
#define BALANCER_VS_OPS_FLAG ((uint64_t)(1ull << 4))

/// Use Pure Round Robin, which means schedule subsequent packets based on
/// monotonic counter (and not based on 5-tuple hash).
#define BALANCER_VS_PRR_FLAG ((uint64_t)(1ull << 5))

////////////////////////////////////////////////////////////////////////////////

// Real server flags

/// If real serves on the IPv6 address.
#define BALANCER_REAL_IPV6_FLAG ((uint64_t)(1ull << 0))

/// If real is enabled.
#define BALANCER_REAL_DISABLED_FLAG ((uint64_t)(1ull << 1))

////////////////////////////////////////////////////////////////////////////////

struct agent;
struct balancer_vs_config;

/// Create new config of the virtual service.
/// @param agent Agent in which memory config will be allocated.
/// @param id Index of the virtual service in the balancer vs registry
/// @param flags Mask of virtual service configuration flags.
/// @param ip IP address of the service (IPv6 if BALANCER_VS_IPV6_FLAG is
/// specified).
/// @param port Port of the virtual service (any if BALANCER_VS_PURE_L3_FLAG is
/// specified).
/// @param proto Transport protocol of the virtual service (TCP or UDP).
/// @param real_count Number of reals which can serve user requests.
/// @param allowed_src_count Number of source subnets allowed by virtual
/// service.
struct balancer_vs_config *
balancer_vs_config_create(
	struct agent *agent,
	size_t id,
	uint64_t flags,
	uint8_t *ip,
	uint16_t port,
	uint8_t proto,
	size_t real_count,
	size_t allowed_src_count,
	size_t peers_v4_count,
	size_t peers_v6_count
);

/// Free virtual service config.
void
balancer_vs_config_free(struct balancer_vs_config *vs_config);

/// Allows to setup one real of the virtual service.
/// @param real_registry_idx Index of the real in the balancer state registry
/// @param idx Index of the real in virtual service list.
/// @param flags Real flags.
/// @param weight Weight of the real (more weight -> more requests to the real).
/// @param dst_addr Address of the real.
/// @param src_addr, src_mask Takes part in the configuration of tunnelled
/// packet source address (result_src = (client_src & ~src_mask) | (src_addr &
/// src_mask)).
void
balancer_vs_config_set_real(
	struct balancer_vs_config *vs_config,
	size_t real_registry_idx,
	size_t idx,
	uint64_t flags,
	uint16_t weight,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
);

/// Set range of the source addresses from which packets are allowed.
void
balancer_vs_config_set_allowed_src_range(
	struct balancer_vs_config *vs_config,
	size_t index,
	uint8_t *from,
	uint8_t *to
);

/// Set address of v4 peer
void
balancer_vs_config_set_peer_v4(
	struct balancer_vs_config *vs_config, size_t index, uint8_t *addr
);

/// Set address of v6 peer
void
balancer_vs_config_set_peer_v6(
	struct balancer_vs_config *vs_config, size_t index, uint8_t *addr
);