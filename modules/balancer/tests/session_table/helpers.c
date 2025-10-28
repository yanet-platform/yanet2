#include "helpers.h"
#include "dataplane/session.h"
#include <netinet/in.h>
#include <time.h>

#include "../utils/rng.h"

////////////////////////////////////////////////////////////////////////////////

struct session_id *
gen_sessions(size_t sessions_cnt, void *memory, uint32_t worker_idx) {
	if (memory == NULL) {
		return NULL;
	}
	struct session_id *sessions = (struct session_id *)memory;
	uint64_t rng = worker_idx * 2 + 5;
	for (size_t i = 0; i < sessions_cnt; ++i) {
		struct session_id *session = &sessions[i];
		if (rng_next(&rng) % 2 == 0) { // ipv4 session
			session->network_proto = IPPROTO_IP;
			memset(session->ip_source, 0, 16);
			memset(session->ip_destination, 0, 16);
			for (size_t j = 0; j < 4; ++j) {
				session->ip_source[j] = rng_next(&rng) & 0xFF;
				session->ip_destination[j] =
					rng_next(&rng) & 0xFF;
			}
		} else { // ipv6 session
			session->network_proto = IPPROTO_IPV6;
			for (size_t j = 0; j < 16; ++j) {
				session->ip_source[j] = rng_next(&rng) & 0xFF;
				session->ip_destination[j] =
					rng_next(&rng) & 0xFF;
			}
		}
		session->transport_proto =
			(rng_next(&rng) % 2 == 0) ? IPPROTO_TCP : IPPROTO_UDP;
		session->port_source = rng_next(&rng) & 0xFFFF;
		session->port_destination = rng_next(&rng) & 0xFFFF;
	}
	return sessions;
}

////////////////////////////////////////////////////////////////////////////////

uint64_t
get_time_ns() {
	struct timespec ts;
	int res = clock_gettime(CLOCK_REALTIME, &ts);
	assert(res == 0);
	return ts.tv_nsec + ts.tv_sec * 1e9;
}