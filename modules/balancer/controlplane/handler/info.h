#pragma once

#include "api/session.h"
#include <stddef.h>

struct packet_handler;
struct balancer_info;

/**
 * Retrieve information about all active sessions in the packet handler.
 *
 * Iterates through the session table and collects information about each
 * session that belongs to a real server present in the current packet handler
 * configuration. Allocates memory for the sessions array which must be freed
 * by the caller.
 *
 * @param handler  Packet handler instance
 * @param sessions Output pointer to allocated array of session info
 *                 (caller must free this memory)
 * @param now      Current timestamp for session timeout calculations
 * @return Number of sessions in the output array
 */
size_t
packet_handler_sessions_info(
	struct packet_handler *handler,
	struct named_session_info **sessions,
	uint32_t now
);

/**
 * Collect comprehensive balancer information including VS and real statistics.
 *
 * Aggregates session counts and timestamps for all virtual services and their
 * real servers. Allocates memory for the info structure's internal arrays
 * which must be freed by the caller.
 *
 * @param handler Packet handler instance
 * @param info    Output structure to populate with balancer information
 *                (caller must free info->vs and nested arrays)
 * @param now     Current timestamp for active session calculations
 */
void
packet_handler_balancer_info(
	struct packet_handler *handler, struct balancer_info *info, uint32_t now
);

void
packet_handler_active_sessions(
	struct packet_handler *handler, struct balancer_info *info
);
