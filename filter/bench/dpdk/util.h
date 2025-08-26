#pragma once

#include "filter/rule.h"
#include "lib/dataplane/packet/packet.h"

#include "def.h"
#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

int
dpdk_init(int argc, char **argv);

int
dpdk_acl_init(
	struct dpdk_acl *acl,
	const struct filter_rule *rules,
	uint32_t rule_count
);

////////////////////////////////////////////////////////////////////////////////

void
dpdk_acl_debug_fields(const uint8_t *data);

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
dpdk_acl_classify(struct dpdk_acl *acl, struct packet *packet) {
	uint32_t result;
	const uint8_t *packet_bytes = (const uint8_t *)packet->mbuf;

	// debug packet bytes used in classification

	int res = rte_acl_classify(acl->ctx, &packet_bytes, &result, 1, 1);
	assert(res == 0);
	return result;
}

////////////////////////////////////////////////////////////////////////////////