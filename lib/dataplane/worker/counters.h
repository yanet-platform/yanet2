#pragma once

struct dp_config;

// Register the five standard worker counters (iterations, rx, tx,
// remote_rx, remote_tx) in dp_config->worker_counters. Caller must
// initialise the registry via counter_registry_init before calling.
//
// Returns 0 on success, -1 if any counter fails to register.
int
worker_counters_register(struct dp_config *dp_config);
