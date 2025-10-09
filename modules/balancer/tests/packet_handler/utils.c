#include "utils.h"
#include "config.h"
#include "state.h"
#include "vs.h"

////////////////////////////////////////////////////////////////////////////////

int
config_data_init(
	struct balancer_module_config *config,
	struct memory_context *mctx,
	size_t workers_cnt
);

////////////////////////////////////////////////////////////////////////////////

struct cp_module *make_balancer(struct memory_context *mctx, size_t workers, struct balancer_state_config *state_cfg) {
    struct balancer_module_config *cfg = memory_balloc(mctx, sizeof(*state_cfg));
    memcpy(&cfg->state_config, state_cfg, sizeof(*state_cfg));
    int res = config_data_init(cfg, mctx, workers);
    if (res != 0) {
        return NULL;
    }
    return &cfg->cp_module;
}