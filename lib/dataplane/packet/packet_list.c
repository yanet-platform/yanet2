#include "packet_list.h"

#include "packet.h"

void
packet_list_print(struct packet_list *list) {
	for (struct packet *pkt = list->first; pkt != NULL; pkt = pkt->next) {
		logtrace_rte_mbuf(packet_to_mbuf(pkt));
	}
}
