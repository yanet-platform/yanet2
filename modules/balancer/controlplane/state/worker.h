#pragma once

#include "common/rcu.h"

#include <assert.h>

#define MAX_WORKERS_NUM 8

static_assert(MAX_WORKERS_NUM <= RCU_WORKERS, "too many workers");
