#pragma once

#include "lib/controlplane/agent/agent.h"

struct mock;
struct agent;

struct mock *
mock_create(size_t memory);

struct mock *
mock_init(void *arena, size_t memory);

void
mock_free(struct mock *mock);

struct agent *
mock_create_agent(struct mock *mock, size_t memory);
