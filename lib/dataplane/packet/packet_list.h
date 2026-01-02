#pragma once

#include <stdlib.h>
#include <strings.h>

#include "lib/dataplane/packet/packet.h"

struct packet_list {
	struct packet *first;
	uint64_t count;
	uint64_t size;
	struct packet **last;
};

static inline void
packet_list_init(struct packet_list *list) {
	memset(list, 0, sizeof(struct packet_list));
	list->last = &list->first;
}

static inline void
packet_list_add(struct packet_list *list, struct packet *packet) {
	*list->last = packet;
	packet->next = NULL;
	list->last = &packet->next;
	list->count += 1;
	list->size += packet_data_len(packet);
}

static inline struct packet *
packet_list_first(struct packet_list *list) {
	return list->first;
}

static inline void
packet_list_concat(struct packet_list *dst, struct packet_list *src) {
	// Nothing to do if src is empty
	if (src->first == NULL)
		return;

	// Replace dst with src if dst is empty
	if (dst->first == NULL) {
		*dst = *src;
		return;
	}

	*dst->last = packet_list_first(src);
	dst->last = src->last;
	dst->count += src->count;
	dst->size += src->size;
}

static inline struct packet *
packet_list_pop(struct packet_list *packets) {
	struct packet *res = packets->first;
	if (res == NULL)
		return res;

	packets->first = res->next;
	if (packets->first == NULL)
		packets->last = &packets->first;
	packets->count -= 1;
	packets->size -= packet_data_len(res);

	return res;
}

static inline uint64_t
packet_list_count(struct packet_list *packets) {
	return packets->count;
}

static inline uint64_t
packet_list_bytes_sum(struct packet_list *list) {
	return list->size;
}

void
packet_list_print(struct packet_list *list);
