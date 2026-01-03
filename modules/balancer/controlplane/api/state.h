#pragma once

#include <stddef.h>

/**
 * Session state storage configuration.
 *
 * Controls sizing of the session table used by the balancer.
 */
struct state_config {
	size_t table_size; // Number of session table buckets/entries
};
