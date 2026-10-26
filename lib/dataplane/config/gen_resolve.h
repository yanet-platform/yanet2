#pragma once

struct config_gen_ectx;

// Derives the absolute addresses a generation's execution context runs
// the packet hot path on and fills its private per-context buffers.
//
// Resolves every stage's counter pointers and hop addresses and each
// object's and module's controlplane counterpart; a buffer is filled by
// the execution-context commit handler its object or module type
// registers, when there is one. Must run in the dataplane process,
// before the context is first released to a worker. Everything is
// recomputed from controlplane-owned data, so a re-run rewrites the same
// values.
void
config_gen_ectx_resolve_counters(struct config_gen_ectx *config_gen_ectx);
