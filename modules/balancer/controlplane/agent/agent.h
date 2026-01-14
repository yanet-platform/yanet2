#pragma once

#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

/**
 * Opaque handle to a balancer agent instance.
 *
 * The agent is the top-level container that manages multiple balancer managers.
 * It coordinates shared memory allocation and provides lifecycle management
 * for balancer instances.
 *
 * Thread-Safety: Not thread-safe. External synchronization required for
 * concurrent access.
 */
struct balancer_agent;

struct yanet_shm;

/**
 * Create a new balancer agent instance.
 *
 * The agent is responsible for managing multiple balancer managers and
 * coordinating their access to shared memory. It allocates the specified
 * amount of memory from the provided shared memory region.
 *
 * @param shm     Pointer to the shared memory region to use for allocations.
 * @param memory  Amount of memory (in bytes) to allocate for the agent.
 * @return Newly created agent handle on success, or NULL on error.
 */
struct balancer_agent *
balancer_agent(struct yanet_shm *shm, size_t memory);

struct balancer_manager;

/**
 * Container for a list of balancer managers.
 *
 * Used to retrieve all managers currently registered with an agent.
 * The managers array is owned by the agent and should not be freed by caller.
 */
struct balancer_managers {
	size_t count;			    // Number of managers in the array
	struct balancer_manager **managers; // Array of manager pointers
};

/**
 * Retrieve all balancer managers registered with the agent.
 *
 * Fills the provided balancer_managers structure with pointers to all
 * currently active managers. The returned array is owned by the agent
 * and remains valid until the agent is destroyed or managers are modified.
 *
 * @param agent     Agent handle.
 * @param managers  Output structure to be filled with manager list.
 */
void
balancer_agent_managers(
	struct balancer_agent *agent, struct balancer_managers *managers
);

struct balancer_manager_config;

/**
 * Create and register a new balancer manager with the agent.
 *
 * Creates a new manager instance with the specified name and configuration,
 * then registers it with the agent. The manager will be included in
 * subsequent calls to balancer_agent_managers().
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_agent_take_error().
 *
 * @param agent   Agent that will own the manager.
 * @param name    Human-readable manager name (used for identification).
 * @param config  Initial configuration for the manager.
 * @return Newly created manager handle on success, or NULL on error.
 */
struct balancer_manager *
balancer_agent_new_manager(
	struct balancer_agent *agent,
	const char *name,
	struct balancer_manager_config *config
);

/**
 * Retrieve the last diagnostic error message for this agent.
 *
 * Returns the most recent error message recorded by agent operations.
 * After calling this function, the error state is cleared.
 *
 * Ownership: The returned string is heap-allocated for the caller; you must
 * free() it when no longer needed. Returns NULL if no error is available.
 *
 * @param agent Agent handle.
 * @return Null-terminated error message string to be freed by caller, or NULL.
 */
const char *
balancer_agent_take_error(struct balancer_agent *agent);