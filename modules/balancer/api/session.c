#include "session.h"

#include "../dataplane/session.h"
#include "lib/controlplane/agent/agent.h"

#include "common/memory.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_sessions_timeouts *
balancer_sessions_timeouts_create(
	struct agent *agent,
	uint32_t tcp_syn_ack,
	uint32_t tcp_syn,
	uint32_t tcp_fin,
	uint32_t tcp,
	uint32_t udp,
	uint32_t default_timeout
) {
	struct balancer_sessions_timeouts *sessions_timeouts = memory_balloc(
		&agent->memory_context, sizeof(*sessions_timeouts)
	);
	if (sessions_timeouts == NULL) {
		return NULL;
	}
	sessions_timeouts->tcp_syn_ack = tcp_syn_ack;
	sessions_timeouts->tcp_syn = tcp_syn;
	sessions_timeouts->tcp_fin = tcp_fin;
	sessions_timeouts->tcp = tcp;
	sessions_timeouts->udp = udp;
	sessions_timeouts->default_timeout = default_timeout;
	return sessions_timeouts;
}