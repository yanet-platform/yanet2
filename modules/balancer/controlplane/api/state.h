#pragma once

#include <stddef.h>
#include <stdint.h>

/**
 * Session table state configuration.
 *
 * Controls the sizing of the session table used to track active connections
 * between clients and real servers. The session table is a hash table that
 * stores session state including client address, selected real server, and
 * timeout information.
 *
 * MEMORY USAGE:
 * Each session entry consumes approximately 64-128 bytes depending on the
 * platform. The actual memory usage is:
 *   memory ≈ table_capacity * sizeof(session_entry) * (1 + overhead)
 * where overhead accounts for hash table load factor and metadata.
 *
 * PERFORMANCE CONSIDERATIONS:
 * - Larger capacity: Lower collision rate, faster lookups, more memory
 * - Smaller capacity: Higher collision rate, slower lookups, less memory
 * - Recommended load factor: 0.7-0.9 (70-90% full before resizing)
 *
 * AUTOMATIC RESIZING:
 * When refresh_period is enabled and session_table_max_load_factor is set,
 * the table automatically doubles in size when:
 *   (active_sessions / table_capacity) > max_load_factor
 *
 * SIZING GUIDELINES:
 * - Expected sessions: Set capacity to expected_sessions / 0.75
 * - High-traffic: Start with 100K-1M capacity
 * - Medium-traffic: Start with 10K-100K capacity
 * - Low-traffic: Start with 1K-10K capacity
 * - Enable auto-resize to handle traffic spikes
 */
struct state_config {
	/**
	 * Maximum number of concurrent sessions the table can hold.
	 *
	 * This is the hash table size, not the maximum number of active
	 * sessions. Due to hash collisions and load factor considerations,
	 * the effective capacity is typically 70-90% of this value.
	 *
	 * CONSTRAINTS:
	 * - Must be > 0
	 * - Should be a power of 2 for optimal hash distribution
	 * - Typical range: 1024 to 10,000,000
	 *
	 * RESIZING:
	 * - Can be changed via balancer_resize_session_table()
	 * - Automatically doubled when load factor exceeds threshold
	 * - Resizing migrates existing sessions to new table
	 */
	size_t table_capacity;
};
