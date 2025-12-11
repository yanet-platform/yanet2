#include "diag.h"

#include <asm-generic/errno-base.h>
#include <errno.h>
#include <stdlib.h>

#include <string.h>

#include "common/tls_stack/stack.h"

void
diag_reset(struct diag *diag) {
	free((void *)diag->error);
	diag->error = NULL;
	diag->has_error = false;
}

void
diag_fill(struct diag *diag) {
	size_t error_len = tls_stack_size();
	if (error_len == 0) {
		// empty
		diag->error = NULL;
		diag->has_error = false;
	} else {
		diag->has_error = true;
		diag->error = NULL;
		char *error = malloc(error_len);
		if (error == NULL) {
			// no mem, so do nothing,
			// will return errno = ENOMEM
			return;
		}
		memcpy(error, tls_stack_pop(error_len), error_len);
		diag->error = error;
	}
}

const char *
diag_msg(struct diag *diag) {
	errno = 0;
	if (!diag->has_error) {
		return NULL;
	} else if (diag->error != NULL) {
		return diag->error;
	} else {
		errno = ENOMEM;
		return NULL;
	}
}

const char *
diag_take_msg(struct diag *diag) {
	const char *msg = diag_msg(diag);
	diag->error = NULL;
	diag->has_error = false;
	return msg;
}