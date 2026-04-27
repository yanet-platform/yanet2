#include "errors.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct yanet_error {
	char *message;
	struct yanet_error *cause;
};

// Static singleton returned when a constructor cannot allocate memory. It is
// distinguished from heap-allocated errors by its address, so
// yanet_error_free() can skip it safely.
//
// It is legal to pass as a cause to yanet_error_wrap().
static yanet_error err_oom = {
	.message = (char *)"out of memory",
	.cause = NULL,
};

static yanet_error *
yanet_error_vwrap(yanet_error *cause, const char *fmt, va_list ap) {
	char *message = NULL;
	if (vasprintf(&message, fmt, ap) < 0) {
		// Preserve the chain; drop the new frame. If there was no
		// chain to begin with, at least surface the OOM.
		return cause != NULL ? cause : &err_oom;
	}

	yanet_error *err = malloc(sizeof(*err));
	if (err == NULL) {
		free(message);
		return cause != NULL ? cause : &err_oom;
	}

	err->message = message;
	err->cause = cause;
	return err;
}

// Wraps an existing error with a new frame that prepends context.
//
// Ownership of `cause` is transferred to the returned error.
//
// If allocation fails, `cause` is returned unchanged: the chain is preserved,
// only the new frame is lost. The `cause` MAY be NULL, in which case this
// behaves like yanet_error_new().
yanet_error *
yanet_error_wrap(yanet_error *cause, const char *fmt, ...) {
	va_list ap;
	va_start(ap, fmt);
	yanet_error *err = yanet_error_vwrap(cause, fmt, ap);
	va_end(ap);
	return err;
}

static yanet_error *
yanet_error_vnew(const char *fmt, va_list ap) {
	return yanet_error_vwrap(NULL, fmt, ap);
}

// Creates a leaf error with a printf-formatted message.
//
// On allocation failure a static out-of-memory singleton is returned, so the
// caller never needs to check the result for NULL.
//
// The returned pointer must eventually be passed to yanet_error_free()
// (directly or by being wrapped into another error).
yanet_error *
yanet_error_new(const char *fmt, ...) {
	va_list ap;
	va_start(ap, fmt);
	yanet_error *err = yanet_error_vwrap(NULL, fmt, ap);
	va_end(ap);
	return err;
}

void
yanet_error_free(yanet_error *err) {
	while (err != NULL && err != &err_oom) {
		yanet_error *cause = err->cause;
		free(err->message);
		free(err);
		err = cause;
	}
}

const char *
yanet_error_message(const yanet_error *err) {
	return err != NULL ? err->message : NULL;
}

const yanet_error *
yanet_error_cause(const yanet_error *err) {
	return err != NULL ? err->cause : NULL;
}

char *
yanet_error_format(const yanet_error *err) {
	if (err == NULL) {
		return NULL;
	}

	// First pass: compute the required buffer size.
	size_t total = 1; // \0 terminator.
	for (const yanet_error *cur = err; cur != NULL; cur = cur->cause) {
		total += strlen(cur->message);
		if (cur->cause != NULL) {
			total += 2; // ": "
		}
	}

	char *out = malloc(total);
	if (out == NULL) {
		return NULL;
	}

	char *p = out;
	char *end = out + total;
	for (const yanet_error *cur = err; cur != NULL; cur = cur->cause) {
		int n = snprintf(p, end - p, "%s", cur->message);
		if (n < 0 || p + n >= end) {
			// Should not happen given the pre-computed size, but
			// bail out gracefully just in case.
			free(out);
			return NULL;
		}
		p += n;
		if (cur->cause != NULL) {
			n = snprintf(p, end - p, ": ");
			if (n < 0 || p + n >= end) {
				free(out);
				return NULL;
			}
			p += n;
		}
	}

	return out;
}

void
yanet_error_add(yanet_error **err, const char *fmt, ...) {
	if (err == NULL) {
		return;
	}

	va_list ap;
	va_start(ap, fmt);

	if (*err == NULL) {
		*err = yanet_error_vnew(fmt, ap);
	} else {
		*err = yanet_error_vwrap(*err, fmt, ap);
	}

	va_end(ap);
}

void
yanet_error_reset(yanet_error **err) {
	if (err == NULL || *err == NULL) {
		return;
	}
	yanet_error_free(*err);
	*err = NULL;
}
