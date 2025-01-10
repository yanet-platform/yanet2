#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>

#include "dataplane/config/zone.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/dscp.h"
#include "dataplane/packet/packet.h"

static int
dscp_handle_v4(struct dscp_module_config *config, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	if (lpm_lookup(&config->lpm_v4, 4, (uint8_t *)&header->dst_addr) !=
	    LPM_VALUE_INVALID) {
		return dscp_mark_v4(header, config->dscp);
	}

	return -1;
}

static int
dscp_handle_v6(struct dscp_module_config *config, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (lpm_lookup(&config->lpm_v6, 16, (uint8_t *)&header->dst_addr) !=
	    LPM_VALUE_INVALID) {
		return dscp_mark_v6(header, config->dscp);
	}

	return -1;
}

static inline int
dscp_handle(struct dscp_module_config *config, struct packet *packet) {
	uint16_t type = packet->network_header.type;
	int result = -1;
	if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		result = dscp_handle_v4(config, packet);
	} else if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		result = dscp_handle_v6(config, packet);
	}
	return result;
}

void
dscp_handle_packets(
	struct dp_config *dp_config,
	struct module_data *module_data,
	struct packet_front *packet_front
) {
	(void)dp_config;
	struct dscp_module_config *dscp_config = container_of(
		module_data, struct dscp_module_config, module_data
	);

	if (dscp_config->dscp.flag != DSCP_MARK_NEVER) {
		struct packet *packet;
		while ((packet = packet_list_pop(&packet_front->input)) != NULL
		) {
			dscp_handle(dscp_config, packet);
			packet_list_add(&packet_front->output, packet);
		}
	} else {
		packet_front_pass(packet_front);
	}

	return;
}

struct dscp_module {
	struct module module;
};

struct module *
new_module_dscp() {
	struct dscp_module *module =
		(struct dscp_module *)malloc(sizeof(struct dscp_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "dscp"
	);
	module->module.handler = dscp_handle_packets;

	return &module->module;
}
