#include "../helper.h"
#include "../rule.h"
#include "dataplane/packet/packet.h"

#include "filter/trie.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

////////////////////////////////////////////////////////////////////////////////

typedef void (*rule_get_net4_func)(
	const struct filter_rule *rule, struct net4 **net, uint32_t *count
);

static inline void
action_get_net4_src(
	const struct filter_rule *rule, struct net4 **net, uint32_t *count
) {
	*net = rule->net4.srcs;
	*count = rule->net4.src_count;
}

static inline void
action_get_net4_dst(
	const struct filter_rule *action, struct net4 **net, uint32_t *count
) {
	*net = action->net4.dsts;
	*count = action->net4.dst_count;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
collect_net4_values(
	struct memory_context *memory_context,
	const struct filter_rule *rules,
	uint32_t count,
	rule_get_net4_func get_net4,
	struct trie *trie,
	struct value_registry *registry
) {

	int ret = trie_init(trie, memory_context);
	if (ret < 0) {
		return -1;
	}

	for (const struct filter_rule *rule = rules; rule < rules + count;
	     ++rule) {
		if (rule->net4.src_count == 0 && rule->net4.dst_count == 0) {
			continue;
		}

		struct net4 *nets;
		uint32_t net_count;
		get_net4(rule, &nets, &net_count);

		for (struct net4 *net4 = nets; net4 < nets + net_count;
		     ++net4) {
			uint32_t addr = htobe32(net4->addr);
			ret = trie_add(
				trie,
				(uint8_t *)&addr,
				__builtin_popcountll(net4->mask),
				rule - rules
			);
			if (ret < 0) {
				return -1;
			}
		}
	}

	// build trie classifiers
	trie_build_classifiers(trie);

	return fill_rule_registry_by_trie(
		trie, count, registry, memory_context
	);
}

////////////////////////////////////////////////////////////////////////////////

// Allows to initialize attribute for IPv4 source address.
static inline int
init_net4_src(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct trie *trie = memory_balloc(memory_context, sizeof(struct trie));
	*data = trie;
	return collect_net4_values(
		memory_context,
		actions,
		actions_count,
		action_get_net4_src,
		trie,
		registry
	);
}

// Allows to lookup classifier for packet IPv4 source address.
static inline uint32_t
lookup_net4_src(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	struct trie *trie = (struct trie *)data;
	uint32_t res = trie_classify(trie, (uint8_t *)&ipv4_hdr->src_addr, 4);
	return res;
}

// Allows to initialize attribute for IPv4 destination address.
static inline int
init_net4_dst(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct trie *trie = memory_balloc(memory_context, sizeof(struct trie));
	*data = trie;
	return collect_net4_values(
		memory_context,
		actions,
		actions_count,
		action_get_net4_dst,
		trie,
		registry
	);
}

// Allows to lookup classifier for packet IPv4 destination address.
static inline uint32_t
lookup_net4_dst(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	struct trie *trie = (struct trie *)data;
	uint32_t res = trie_classify(trie, (uint8_t *)&ipv4_hdr->dst_addr, 4);
	return res;
}

// Allows to free data for IPv4 classification.
static inline void
free_net4(void *data, struct memory_context *memory_context) {
	(void)memory_context;
	struct trie *trie = (struct trie *)data;
	trie_free_mem(trie);
}