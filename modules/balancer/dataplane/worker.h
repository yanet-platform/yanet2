#pragma once

#include "flow/context.h"
#include <threads.h>

#define MAX_WORKERS_NUM 8

enum { batch_size = 32 };

static thread_local struct packet_ctx packet_ctxs[batch_size];