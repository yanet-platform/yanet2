#pragma once

#include <stdint.h>

struct filter;
struct packet;

uint32_t filter_classify_src_net_hi(
	const struct filter *filter, const struct packet *packet
);

uint32_t filter_classify_src_net_lo(
	const struct filter *filter, const struct packet *packet
);

uint32_t filter_classify_dst_net_hi(
	const struct filter *filter, const struct packet *packet
);

uint32_t filter_classify_dst_net_lo(
	const struct filter *filter, const struct packet *packet
);

uint32_t filter_classify_src_port(
	const struct filter *filter, const struct packet *packet
);

uint32_t filter_classify_dst_port(
	const struct filter *filter, const struct packet *packet
);
