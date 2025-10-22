#include "filter/filter.h"
#include "module.h"
#include "ring.h"

////////////////////////////////////////////////////////////////////////////////

typedef uint8_t vs_flags_t;

#define VS_PURE_L3_FLAG ((vs_flags_t)(1u << 0))
#define VS_IPV6_FLAG ((vs_flags_t)(1u << 1))

#define VS_FIX_MSS_FLAG ((vs_flags_t)(1u << 2))
#define VS_GRE_FLAG ((vs_flags_t)(1u << 3))

#define VS_OPS_FLAG ((vs_flags_t)(1u << 4))

////////////////////////////////////////////////////////////////////////////////

struct virtual_service {
	vs_flags_t flags;

	uint8_t address[16];

	uint16_t port;
	uint8_t proto;

	uint64_t real_start;
	uint64_t real_count;

	struct lpm src_filter;

	struct ring real_ring;
};

////////////////////////////////////////////////////////////////////////////////

#define VS_V4_TABLE_TAG __VS_V4_TABLE_TAG

FILTER_DECLARE(
	VS_V4_TABLE_TAG,
	&attribute_net4_dst,
	&attribute_port_dst,
	&attribute_proto
);

static inline uint32_t
vs_v4_table_lookup(
	struct balancer_module_config *config, struct packet *packet
) {
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(
		&config->vs_v4_table,
		VS_V4_TABLE_TAG,
		packet,
		&actions,
		&actions_count
	);
	if (actions_count == 0) {
		return -1;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = actions[0];
	return service_id;
}

////////////////////////////////////////////////////////////////////////////////

#define VS_V6_TABLE_TAG __VS_V6_TABLE_TAG

FILTER_DECLARE(
	VS_V6_TABLE_TAG,
	&attribute_net6_dst,
	&attribute_port_dst,
	&attribute_proto
);

static inline uint32_t
vs_v6_table_lookup(
	struct balancer_module_config *config, struct packet *packet
) {
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(
		&config->vs_v6_table,
		VS_V6_TABLE_TAG,
		packet,
		&actions,
		&actions_count
	);
	if (actions_count == 0) {
		return -1;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = actions[0];
	return service_id;
}
