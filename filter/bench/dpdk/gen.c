#include "gen.h"
#include "rule.h"
#include "utils.h"
#include <netinet/in.h>

////////////////////////////////////////////////////////////////////////////////

void
generate_rules(struct filter_rule_holder *holder, uint32_t count) {
	for (uint32_t i = 0; i < count; ++i) {
		struct filter_rule *rule = &holder->rules[i];

		holder->nets_src[i].addr = ip(0xff, 0xff, i & 0xff, 0);
		holder->nets_src[i].mask = ip(0xff, 0xff, 0xff, 0);

		holder->nets_dst[i].addr = ip(0xff, 0xff, (i + 1) & 0xff, 0);
		holder->nets_dst[i].mask = ip(0xff, 0xff, 0xff, 0);

		rule->net4.dst_count = rule->net4.src_count = 1;
		rule->net4.srcs = &holder->nets_src[i];
		rule->net4.dsts = &holder->nets_dst[i];

		holder->ports_src[i] = (struct filter_port_range
		){.from = i & 127, .to = 500 + (i & 255)};
		holder->ports_dst[i] = (struct filter_port_range
		){.from = 100 + (i & 127), .to = 600 + (i & 255)};

		rule->transport.src_count = rule->transport.dst_count = 1;
		rule->transport.srcs = &holder->ports_src[i];
		rule->transport.dsts = &holder->ports_dst[i];

		rule->transport.proto = (struct filter_proto
		){.proto = IPPROTO_UDP, .enable_bits = 0, .disable_bits = 0};

		rule->action = i + 1;
	}
}

////////////////////////////////////////////////////////////////////////////////