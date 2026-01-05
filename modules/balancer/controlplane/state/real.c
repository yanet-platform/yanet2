#include "real.h"

uint16_t
real_weight(struct real_state *state) {
	return state->enabled ? state->weight : 0;
}