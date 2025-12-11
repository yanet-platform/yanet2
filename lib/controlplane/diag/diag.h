#pragma once

#include <stdbool.h>

struct diag {
    bool has_error;
    const char *error;
};

void
diag_fill(struct diag *diag);

const char *
diag_msg(struct diag *diag);

const char *
diag_take_msg(struct diag *diag);

void
diag_reset(struct diag *diag);