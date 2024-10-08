#include "ipfw.h"

#include <stdlib.h>

#include <endian.h>

int
main(int argc, char **argv)
{
	(void) argc;
	(void) argv;

	struct ipfw_filter_action *actions;

	actions = (struct ipfw_filter_action *)
		malloc(sizeof(struct ipfw_filter_action) * 2);

	actions[0].filter.net6.src_count = 2;
	actions[0].filter.net6.srcs = (struct ipfw_net6 *)
		malloc(sizeof(struct ipfw_net6) * 2);
	actions[0].filter.net6.srcs[0] =
		(struct ipfw_net6){0, 0, 0x00000000000000C0, 0};
	actions[0].filter.net6.srcs[1] =
		(struct ipfw_net6){0x80, 0, 0x0000000000000080, 0};
	actions[0].filter.net6.dst_count = 1;
	actions[0].filter.net6.dsts = (struct ipfw_net6 *)
		malloc(sizeof(struct ipfw_net6) * 1);
	actions[0].filter.net6.dsts[0] =
		(struct ipfw_net6){0x0000000000000080, 0, 0x0000000000000080, 0};

	actions[0].filter.transport.src_count = 1;
	actions[0].filter.transport.srcs = (struct ipfw_port_range *)
		malloc(sizeof(struct ipfw_port_range) * 1);
	actions[0].filter.transport.srcs[0] =
		(struct ipfw_port_range){0, 65535};

	actions[0].filter.transport.dst_count = 2;
	actions[0].filter.transport.dsts = (struct ipfw_port_range *)
		malloc(sizeof(struct ipfw_port_range) * 2);
	actions[0].filter.transport.dsts[0] =
		(struct ipfw_port_range){htobe16(80), htobe16(80)};
	actions[0].filter.transport.dsts[1] =
		(struct ipfw_port_range){htobe16(443), htobe16(443)};

	actions[0].action = 0;



	actions[1].filter.net6.src_count = 1;
	actions[1].filter.net6.srcs = (struct ipfw_net6 *)
		malloc(sizeof(struct ipfw_net6) * 1);
	actions[1].filter.net6.srcs[0] =
		(struct ipfw_net6){0, 0, 0, 0};
	actions[1].filter.net6.dst_count = 1;
	actions[1].filter.net6.dsts = (struct ipfw_net6 *)
		malloc(sizeof(struct ipfw_net6) * 1);
	actions[1].filter.net6.dsts[0] =
		(struct ipfw_net6){0, 0, 0, 0};

	actions[1].filter.transport.src_count = 1;
	actions[1].filter.transport.srcs = (struct ipfw_port_range *)
		malloc(sizeof(struct ipfw_port_range) * 1);
	actions[1].filter.transport.srcs[0] =
		(struct ipfw_port_range){0, 65535};

	actions[1].filter.transport.dst_count = 1;
	actions[1].filter.transport.dsts = (struct ipfw_port_range *)
		malloc(sizeof(struct ipfw_port_range) * 1);
	actions[1].filter.transport.dsts[0] =
		(struct ipfw_port_range){0, 65535};

	actions[1].action = 1;


	struct ipfw_packet_filter filter;

	ipfw_packet_filter_create(actions, 2, &filter);

	return 0;
}
