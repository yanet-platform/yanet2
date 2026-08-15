#include "counter_pattern.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "rure.h"

static const char metachars[] = "\\.+*?()|[]{}^$";

static bool
is_literal(const char *text, size_t len) {
	for (size_t idx = 0; idx < len; ++idx) {
		if (strchr(metachars, text[idx]) != NULL) {
			return false;
		}
	}

	return true;
}

static bool
set_literal(
	struct counter_pattern *out,
	enum counter_pattern_kind kind,
	const char *text,
	size_t len
) {
	if (len >= COUNTER_NAME_LEN) {
		return false;
	}

	memcpy(out->literal, text, len);
	out->literal[len] = '\0';
	out->literal_len = len;
	out->kind = kind;

	return true;
}

static bool
compile_fast_path(
	struct counter_pattern *out, const char *pattern, size_t len
) {
	if (len == 2 && pattern[0] == '.' && pattern[1] == '*') {
		out->kind = COUNTER_PATTERN_ANY;
		return true;
	}

	if (is_literal(pattern, len)) {
		return set_literal(out, COUNTER_PATTERN_EXACT, pattern, len);
	}

	if (len > 2 && pattern[len - 2] == '.' && pattern[len - 1] == '*' &&
	    is_literal(pattern, len - 2)) {
		return set_literal(
			out, COUNTER_PATTERN_PREFIX, pattern, len - 2
		);
	}

	if (len > 2 && pattern[0] == '.' && pattern[1] == '*' &&
	    is_literal(pattern + 2, len - 2)) {
		return set_literal(
			out, COUNTER_PATTERN_SUFFIX, pattern + 2, len - 2
		);
	}

	return false;
}

static bool
compile_engine(
	struct counter_pattern *out,
	const char *pattern,
	size_t len,
	char *err,
	size_t err_len
) {
	static const char open[] = "\\A(?:";
	static const char close[] = ")\\z";

	char anchored[sizeof(open) + COUNTER_PATTERN_MAX + sizeof(close)];
	int written = snprintf(
		anchored,
		sizeof(anchored),
		"%s%.*s%s",
		open,
		(int)len,
		pattern,
		close
	);
	if (written < 0 || (size_t)written >= sizeof(anchored)) {
		snprintf(err, err_len, "pattern too long");
		return false;
	}

	rure_error *rerr = rure_error_new();
	rure *compiled = rure_compile(
		(const uint8_t *)anchored,
		strlen(anchored),
		RURE_DEFAULT_FLAGS,
		NULL,
		rerr
	);
	if (compiled == NULL) {
		snprintf(err, err_len, "%s", rure_error_message(rerr));
		rure_error_free(rerr);
		return false;
	}
	rure_error_free(rerr);

	out->kind = COUNTER_PATTERN_ENGINE;
	out->engine = compiled;

	return true;
}

bool
counter_pattern_compile(
	struct counter_pattern *out,
	const char *pattern,
	char *err,
	size_t err_len
) {
	memset(out, 0, sizeof(*out));

	size_t len = strnlen(pattern, COUNTER_PATTERN_MAX + 1);
	if (len > COUNTER_PATTERN_MAX) {
		snprintf(
			err,
			err_len,
			"pattern longer than %d bytes",
			COUNTER_PATTERN_MAX
		);
		return false;
	}

	if (compile_fast_path(out, pattern, len)) {
		return true;
	}

	return compile_engine(out, pattern, len, err, err_len);
}

void
counter_pattern_free(struct counter_pattern *pattern) {
	if (pattern->engine != NULL) {
		rure_free((rure *)pattern->engine);
		pattern->engine = NULL;
	}
}

bool
counter_pattern_match(const struct counter_pattern *pattern, const char *name) {
	switch (pattern->kind) {
	case COUNTER_PATTERN_ANY:
		return true;

	case COUNTER_PATTERN_EXACT:
		return strncmp(name, pattern->literal, COUNTER_NAME_LEN) == 0;

	case COUNTER_PATTERN_PREFIX:
		return strncmp(name, pattern->literal, pattern->literal_len) ==
		       0;

	case COUNTER_PATTERN_SUFFIX: {
		size_t len = strnlen(name, COUNTER_NAME_LEN);
		if (len < pattern->literal_len) {
			return false;
		}

		return memcmp(name + len - pattern->literal_len,
			      pattern->literal,
			      pattern->literal_len) == 0;
	}

	case COUNTER_PATTERN_ENGINE:
		return rure_is_match(
			(rure *)pattern->engine,
			(const uint8_t *)name,
			strnlen(name, COUNTER_NAME_LEN),
			0
		);

	default:
		return false;
	}
}

void
counter_pattern_set_match_all(struct counter_pattern_set *set) {
	memset(set, 0, sizeof(*set));
	set->match_all = true;
}

enum counter_pattern_result
counter_pattern_set_compile(
	struct counter_pattern_set *set,
	const char *const *patterns,
	size_t count,
	char *err,
	size_t err_len
) {
	memset(set, 0, sizeof(*set));

	if (count == 0) {
		return COUNTER_PATTERN_OK;
	}

	set->patterns = calloc(count, sizeof(*set->patterns));
	if (set->patterns == NULL) {
		snprintf(
			err, err_len, "failed to allocate %zu patterns", count
		);
		return COUNTER_PATTERN_NOMEM;
	}

	size_t engines = 0;
	for (size_t idx = 0; idx < count; ++idx) {
		char reason[192] = {0};
		if (!counter_pattern_compile(
			    &set->patterns[idx],
			    patterns[idx],
			    reason,
			    sizeof(reason)
		    )) {
			snprintf(
				err,
				err_len,
				"pattern %zu: %s (starts '%.48s')",
				idx,
				reason,
				patterns[idx]
			);
			set->count = idx;
			counter_pattern_set_free(set);
			return COUNTER_PATTERN_REJECTED;
		}

		set->count = idx + 1;

		if (set->patterns[idx].kind != COUNTER_PATTERN_ENGINE) {
			continue;
		}

		if (++engines > COUNTER_PATTERN_MAX_ENGINE) {
			snprintf(
				err,
				err_len,
				"more than %d patterns need the regex engine",
				COUNTER_PATTERN_MAX_ENGINE
			);
			counter_pattern_set_free(set);
			return COUNTER_PATTERN_REJECTED;
		}
	}

	return COUNTER_PATTERN_OK;
}

void
counter_pattern_set_free(struct counter_pattern_set *set) {
	for (size_t idx = 0; idx < set->count; ++idx) {
		counter_pattern_free(&set->patterns[idx]);
	}

	free(set->patterns);
	set->patterns = NULL;
	set->count = 0;
}

bool
counter_pattern_set_match(
	const struct counter_pattern_set *set, const char *name
) {
	if (set->match_all) {
		return true;
	}

	for (size_t idx = 0; idx < set->count; ++idx) {
		if (counter_pattern_match(&set->patterns[idx], name)) {
			return true;
		}
	}

	return false;
}
