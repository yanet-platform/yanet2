#include "balancer.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "controlplane/diag/diag.h"
#include "handler.h"
#include "vs.h"
#include <assert.h>
#include <stdlib.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

enum alloc_strategy {
	block = 0,
	libc = 1,
};

static inline enum alloc_strategy
opposite_strategy(enum alloc_strategy strategy) {
	return (strategy == block ? libc : block);
}

////////////////////////////////////////////////////////////////////////////////

#define SET_PTR(s, ptr, strategy)                                              \
	do {                                                                   \
		switch (strategy) {                                            \
		case block:                                                    \
			SET_OFFSET_OF(&(s), ptr);                              \
			break;                                                 \
		case libc:                                                     \
			s = ptr;                                               \
		}                                                              \
	} while (0)

#define GET_PTR(s, strategy)                                                   \
	__extension__({                                                        \
		typeof(s) res = NULL;                                          \
		switch (strategy) {                                            \
		case block:                                                    \
			res = s;                                               \
			break;                                                 \
		case libc:                                                     \
			res = ADDR_OF(&(s));                                   \
		}                                                              \
		res;                                                           \
	})

////////////////////////////////////////////////////////////////////////////////

static void
free_internal_vs_config(
	struct named_vs_config *vs, struct memory_context *mctx
) {
	struct vs_config *config = &vs->config;
	memory_bfree(
		mctx,
		ADDR_OF(&config->allowed_src),
		sizeof(config->allowed_src[0]) * config->allowed_src_count
	);
	memory_bfree(
		mctx,
		ADDR_OF(&config->reals),
		sizeof(config->reals[0]) * config->real_count
	);
	memory_bfree(
		mctx,
		ADDR_OF(&config->peers_v4),
		sizeof(config->peers_v4[0]) * config->peers_v4_count
	);
	memory_bfree(
		mctx,
		ADDR_OF(&config->peers_v6),
		sizeof(config->peers_v6[0]) * config->peers_v6_count
	);
}

static void
free_user_vs_config(struct named_vs_config *vs) {
	struct vs_config *config = &vs->config;
	free(config->allowed_src);
	free(config->reals);
	free(config->peers_v4);
	free(config->peers_v6);
}

static void
free_internal_packet_handler_config(
	struct packet_handler_config *config, struct memory_context *mctx
) {
	struct named_vs_config *vs_config = ADDR_OF(&config->vs);
	for (size_t i = 0; i < config->vs_count; ++i) {
		free_internal_vs_config(&vs_config[i], mctx);
	}
	memory_bfree(mctx, vs_config, sizeof(*vs_config) * config->vs_count);
	memory_bfree(
		mctx,
		ADDR_OF(&config->decap_v4),
		config->decap_v4_count * sizeof(config->decap_v4[0])
	);
	memory_bfree(
		mctx,
		ADDR_OF(&config->decap_v6),
		config->decap_v6_count * sizeof(config->decap_v6[0])
	);
}

static void
free_user_packet_handler_config(struct packet_handler_config *config) {
	struct named_vs_config *vs_config = config->vs;
	for (size_t i = 0; i < config->vs_count; ++i) {
		free_user_vs_config(&vs_config[i]);
	}
	free(vs_config);
	free(config->decap_v4);
	free(config->decap_v6);
}

static void
free_packet_handler_config(
	struct packet_handler_config *config,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	switch (strategy) {
	case block:
		free_internal_packet_handler_config(config, mctx);
		break;
	case libc:
		free_user_packet_handler_config(config);
	}
}

void
free_internal_balancer_config(
	struct balancer_config *config, struct memory_context *mctx
) {
	free_internal_packet_handler_config(&config->handler, mctx);
	memory_bfree(mctx, config, sizeof(*config));
}

void
balancer_free_config(struct balancer_config *config) {
	free_user_packet_handler_config(&config->handler);
}

////////////////////////////////////////////////////////////////////////////////

static void *
common_alloc(
	size_t elem_size,
	size_t count,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	if (strategy == block) {
		return memory_balloc(mctx, elem_size * count);
	} else {
		return malloc(elem_size * count);
	}
}

static void
common_free(
	void *ptr,
	size_t size,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	if (strategy == block) {
		memory_bfree(mctx, ptr, size);
	} else {
		free(ptr);
	}
}

////////////////////////////////////////////////////////////////////////////////

static void
free_vs_config(
	struct named_vs_config *config,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	if (strategy == block) {
		free_internal_vs_config(config, mctx);
	} else {
		free_user_vs_config(config);
	}
}

static int
vs_clone_reals(
	struct named_vs_config *dst,
	struct named_vs_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	struct named_real_config *reals = common_alloc(
		sizeof(struct named_real_config),
		src->config.real_count,
		strategy,
		mctx
	);
	if (reals == NULL) {
		NEW_ERROR("failed to allocate storage for reals config");
		return -1;
	}
	dst->config.real_count = src->config.real_count;
	SET_PTR(dst->config.reals, reals, strategy);
	struct named_real_config *src_reals =
		GET_PTR(src->config.reals, opposite_strategy(strategy));
	memcpy(reals, src_reals, sizeof(*src_reals) * dst->config.real_count);
	return 0;
}

static int
vs_clone_allowed_src(
	struct named_vs_config *dst,
	struct named_vs_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	struct net_addr_range *allowed_sources = common_alloc(
		sizeof(struct net_addr_range),
		src->config.allowed_src_count,
		strategy,
		mctx
	);
	if (allowed_sources == NULL) {
		NEW_ERROR("failed to allocate storage for allowed sources");
		return -1;
	}
	dst->config.allowed_src_count = src->config.allowed_src_count;
	SET_PTR(dst->config.allowed_src, allowed_sources, strategy);
	struct net_addr_range *src_allowed_sources =
		GET_PTR(src->config.allowed_src, opposite_strategy(strategy));
	memcpy(allowed_sources,
	       src_allowed_sources,
	       sizeof(*allowed_sources) * dst->config.allowed_src_count);
	return 0;
}

static int
vs_clone_peers_v4(
	struct named_vs_config *dst,
	struct named_vs_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	struct net4_addr *peers_v4 = common_alloc(
		sizeof(struct net4_addr),
		src->config.peers_v4_count,
		strategy,
		mctx
	);
	if (peers_v4 == NULL) {
		NEW_ERROR("failed to allocate storage for IPv4 peers");
		return -1;
	}
	dst->config.peers_v4_count = src->config.peers_v4_count;
	SET_PTR(dst->config.peers_v4, peers_v4, strategy);
	struct net4_addr *src_peers_v4 =
		GET_PTR(src->config.peers_v4, opposite_strategy(strategy));
	memcpy(peers_v4,
	       src_peers_v4,
	       sizeof(*peers_v4) * dst->config.peers_v4_count);
	return 0;
}

static int
vs_clone_peers_v6(
	struct named_vs_config *dst,
	struct named_vs_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	struct net6_addr *peers_v6 = common_alloc(
		sizeof(struct net6_addr),
		src->config.peers_v6_count,
		strategy,
		mctx
	);
	if (peers_v6 == NULL) {
		NEW_ERROR("failed to allocate storage for IPv6 peers");
		return -1;
	}
	dst->config.peers_v6_count = src->config.peers_v6_count;
	SET_PTR(dst->config.peers_v6, peers_v6, strategy);
	struct net6_addr *src_peers_v6 =
		GET_PTR(src->config.peers_v6, opposite_strategy(strategy));
	memcpy(peers_v6,
	       src_peers_v6,
	       sizeof(*peers_v6) * dst->config.peers_v6_count);
	return 0;
}

static int
clone_vs_config(
	struct named_vs_config *dst,
	struct named_vs_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	memset(dst, 0, sizeof(*dst));
	dst->identifier = src->identifier;

	if ((src->config.flags & VS_OPS_FLAG) && src->identifier.port != 0) {
		NEW_ERROR("OPS flag is enabled, but port not equals 0");
		return -1;
	}

	memset(&dst->config, 0, sizeof(dst->config));

	if (vs_clone_reals(dst, src, strategy, mctx) != 0) {
		goto error;
	}

	if (vs_clone_allowed_src(dst, src, strategy, mctx) != 0) {
		goto error;
	}

	if (vs_clone_peers_v4(dst, src, strategy, mctx) != 0) {
		goto error;
	}

	if (vs_clone_peers_v6(dst, src, strategy, mctx) != 0) {
		goto error;
	}

	dst->config.scheduler = src->config.scheduler;
	dst->config.flags = src->config.flags;
	dst->config.user = src->config.user;

error:
	free_vs_config(dst, strategy, mctx);
	return -1;
}

static int
clone_handler_config_vs(
	struct packet_handler_config *dst,
	struct packet_handler_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	struct named_vs_config *vs = common_alloc(
		sizeof(struct named_vs_config), src->vs_count, strategy, mctx
	);
	if (vs == NULL) {
		NEW_ERROR("failed to allocate memory for virtual services");
		return -1;
	}
	struct named_vs_config *vs_src = GET_PTR(src->vs, strategy);
	for (size_t i = 0; i < src->vs_count; ++i) {
		if (clone_vs_config(&vs[i], &vs_src[i], strategy, mctx) != 0) {
			PUSH_ERROR(
				"failed to clone virtual service config at "
				"index %zu",
				i
			);
			for (size_t j = 0; j < i; ++j) {
				free_vs_config(&vs[j], strategy, mctx);
			}
			common_free(vs, src->vs_count, strategy, mctx);
			return -1;
		}
	}
	dst->vs_count = src->vs_count;
	SET_PTR(dst->vs, vs, strategy);
	return 0;
}

static int
clone_handler_config_decap_v4(
	struct packet_handler_config *dst,
	struct packet_handler_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	struct net4_addr *decap_v4 = common_alloc(
		sizeof(struct net4_addr), src->decap_v4_count, strategy, mctx
	);
	if (decap_v4 == NULL) {
		NEW_ERROR("failed to allocate memory for IPv4 decap addresses");
		return -1;
	}
	struct net4_addr *decap_v4_src =
		GET_PTR(src->decap_v4, opposite_strategy(strategy));
	memcpy(decap_v4,
	       decap_v4_src,
	       sizeof(struct net4_addr) * src->decap_v4_count);
	SET_PTR(dst->decap_v4, decap_v4, strategy);
	dst->decap_v4_count = src->decap_v4_count;
	return 0;
}

static int
clone_handler_config_decap_v6(
	struct packet_handler_config *dst,
	struct packet_handler_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	struct net6_addr *decap_v6 = common_alloc(
		sizeof(struct net6_addr), src->decap_v6_count, strategy, mctx
	);
	if (decap_v6 == NULL) {
		NEW_ERROR("failed to allocate memory for IPv6 decap addresses");
		return -1;
	}
	struct net6_addr *decap_v6_src =
		GET_PTR(src->decap_v6, opposite_strategy(strategy));
	memcpy(decap_v6,
	       decap_v6_src,
	       sizeof(struct net6_addr) * src->decap_v6_count);
	SET_PTR(dst->decap_v6, decap_v6, strategy);
	dst->decap_v6_count = src->decap_v6_count;
	return 0;
}

static int
clone_handler_config(
	struct packet_handler_config *dst,
	struct packet_handler_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	memset(dst, 0, sizeof(*dst));

	dst->sessions_timeouts = src->sessions_timeouts;

	if (clone_handler_config_vs(dst, src, strategy, mctx) != 0) {
		PUSH_ERROR("failed to clone virtual services");
		goto error;
	}

	dst->source_v4 = src->source_v4;
	dst->source_v6 = src->source_v6;

	if (clone_handler_config_decap_v4(dst, src, strategy, mctx) != 0) {
		PUSH_ERROR("failed to clone IPv4 decap addresses");
		goto error;
	}

	if (clone_handler_config_decap_v6(dst, src, strategy, mctx) != 0) {
		PUSH_ERROR("failed to clone IPv6 decap addresses");
		goto error;
	}

	return 0;

error:
	free_packet_handler_config(dst, strategy, mctx);

	return -1;
}

static int
clone_config(
	struct balancer_config *dst,
	struct balancer_config *src,
	enum alloc_strategy strategy,
	struct memory_context *mctx
) {
	// state config
	dst->state = src->state;

	// packet handler config
	if (clone_handler_config(
		    &dst->handler, &src->handler, strategy, mctx
	    ) != 0) {
		PUSH_ERROR("failed to clone packet handler config");
		return -1;
	}

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_read_config(struct balancer_config *dst, struct balancer_config *src) {
	int res = clone_config(dst, src, libc, NULL);
	assert(res == 0);
}

int
balancer_setup_config(
	struct balancer_config *dst,
	struct balancer_config *src,
	struct memory_context *mctx
) {
	int res = clone_config(dst, src, block, mctx);
	if (res != 0) {
		PUSH_ERROR("failed to clone config");
		return -1;
	}
	return 0;
}