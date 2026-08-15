#include <stdlib.h>
#include <string.h>

#include "common/test_assert.h"
#include "lib/counters/counter_pattern.h"
#include "lib/logging/log.h"

static int
expect_kind(const char *pattern, enum counter_pattern_kind want) {
	struct counter_pattern compiled;
	char err[256] = {0};

	TEST_ASSERT(
		counter_pattern_compile(&compiled, pattern, err, sizeof(err)),
		"pattern '%s' did not compile: %s",
		pattern,
		err
	);

	enum counter_pattern_kind got = compiled.kind;
	counter_pattern_free(&compiled);

	TEST_ASSERT_EQUAL(
		got, want, "pattern '%s' reduced to wrong kind", pattern
	);

	return TEST_SUCCESS;
}

static int
expect_match(const char *pattern, const char *name, bool want) {
	struct counter_pattern compiled;
	char err[256] = {0};

	TEST_ASSERT(
		counter_pattern_compile(&compiled, pattern, err, sizeof(err)),
		"pattern '%s' did not compile: %s",
		pattern,
		err
	);

	bool got = counter_pattern_match(&compiled, name);
	counter_pattern_free(&compiled);

	TEST_ASSERT_EQUAL(
		got, want, "pattern '%s' vs name '%s'", pattern, name
	);

	return TEST_SUCCESS;
}

static int
test_fast_path_kinds() {
	TEST_ASSERT_SUCCESS(
		expect_kind("acl_no_match", COUNTER_PATTERN_EXACT), "exact"
	);
	TEST_ASSERT_SUCCESS(
		expect_kind("rule 7", COUNTER_PATTERN_EXACT),
		"exact with a space"
	);
	TEST_ASSERT_SUCCESS(
		expect_kind("acl_.*", COUNTER_PATTERN_PREFIX), "prefix"
	);
	TEST_ASSERT_SUCCESS(
		expect_kind(".*_drop", COUNTER_PATTERN_SUFFIX), "suffix"
	);
	TEST_ASSERT_SUCCESS(
		expect_kind(".*", COUNTER_PATTERN_ANY), "catch-all"
	);

	return TEST_SUCCESS;
}

static int
test_fast_path_matching() {
	TEST_ASSERT_SUCCESS(
		expect_match("acl_no_match", "acl_no_match", true), "exact hit"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("acl_no_match", "acl_no_match_extra", false),
		"exact must not match a longer name"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("acl_.*", "acl_sync_sent", true), "prefix hit"
	);
	TEST_ASSERT_SUCCESS(expect_match("acl_.*", "rx", false), "prefix miss");
	TEST_ASSERT_SUCCESS(
		expect_match(".*_drop", "input_drop", true), "suffix hit"
	);
	TEST_ASSERT_SUCCESS(
		expect_match(".*_drop", "drop_input", false),
		"suffix must not match the literal elsewhere"
	);
	TEST_ASSERT_SUCCESS(
		expect_match(".*_drop", "drop", false),
		"suffix must not match a name shorter than the literal"
	);
	TEST_ASSERT_SUCCESS(expect_match(".*", "anything", true), "catch-all");

	return TEST_SUCCESS;
}

static int
test_engine_pattern_count_is_capped() {
	const char *engines[COUNTER_PATTERN_MAX_ENGINE + 1];
	for (size_t idx = 0; idx < COUNTER_PATTERN_MAX_ENGINE + 1; ++idx) {
		engines[idx] = "[a-z]+_drop";
	}

	struct counter_pattern_set set;
	char err[256] = {0};

	TEST_ASSERT(
		counter_pattern_set_compile(
			&set,
			engines,
			COUNTER_PATTERN_MAX_ENGINE + 1,
			err,
			sizeof(err)
		) == COUNTER_PATTERN_REJECTED,
		"an over-long engine list was accepted"
	);
	TEST_ASSERT_STR_CONTAINS(
		err, "regex engine", "error should cite the engine limit"
	);
	TEST_ASSERT_NULL(
		set.patterns, "a rejected list must leave nothing allocated"
	);

	TEST_ASSERT(
		counter_pattern_set_compile(
			&set,
			engines,
			COUNTER_PATTERN_MAX_ENGINE,
			err,
			sizeof(err)
		) == COUNTER_PATTERN_OK,
		"a list at the limit was rejected: %s",
		err
	);
	counter_pattern_set_free(&set);

	const char *exact[COUNTER_PATTERN_MAX_ENGINE * 4];
	char names[COUNTER_PATTERN_MAX_ENGINE * 4][16];
	for (size_t idx = 0; idx < COUNTER_PATTERN_MAX_ENGINE * 4; ++idx) {
		snprintf(names[idx], sizeof(names[idx]), "rule_%zu", idx);
		exact[idx] = names[idx];
	}

	TEST_ASSERT(
		counter_pattern_set_compile(
			&set,
			exact,
			COUNTER_PATTERN_MAX_ENGINE * 4,
			err,
			sizeof(err)
		) == COUNTER_PATTERN_OK,
		"a long exact-name list was rejected: %s",
		err
	);
	TEST_ASSERT(
		counter_pattern_set_match(&set, "rule_200"),
		"a long exact-name list must still match"
	);
	counter_pattern_set_free(&set);

	return TEST_SUCCESS;
}

static int
test_overlong_pattern_is_rejected() {
	size_t len = COUNTER_PATTERN_MAX + 64;
	char *pattern = malloc(len + 4);
	TEST_ASSERT_NOT_NULL(pattern, "malloc failed");

	memset(pattern, 'a', len);
	memcpy(pattern + len, "|.*", 4);

	struct counter_pattern compiled;
	char err[256] = {0};
	bool accepted =
		counter_pattern_compile(&compiled, pattern, err, sizeof(err));
	if (accepted) {
		counter_pattern_free(&compiled);
	}
	free(pattern);

	TEST_ASSERT(!accepted, "overlong pattern was accepted");
	TEST_ASSERT_STR_CONTAINS(
		err, "longer than", "rejection should cite the length limit"
	);

	return TEST_SUCCESS;
}

static int
test_pattern_at_the_limit_compiles() {
	char *pattern = malloc(COUNTER_PATTERN_MAX + 1);
	TEST_ASSERT_NOT_NULL(pattern, "malloc failed");

	memset(pattern, 'a', COUNTER_PATTERN_MAX);
	pattern[COUNTER_PATTERN_MAX] = '\0';

	struct counter_pattern compiled;
	char err[256] = {0};
	bool accepted =
		counter_pattern_compile(&compiled, pattern, err, sizeof(err));
	if (accepted) {
		counter_pattern_free(&compiled);
	}
	free(pattern);

	TEST_ASSERT(accepted, "pattern at the limit was rejected");

	return TEST_SUCCESS;
}

static int
test_pattern_set() {
	struct counter_pattern_set set;
	char err[256] = {0};
	const char *patterns[] = {"acl_.*", "rule 7"};

	TEST_ASSERT(
		counter_pattern_set_compile(
			&set, patterns, 2, err, sizeof(err)
		) == COUNTER_PATTERN_OK,
		"set did not compile: %s",
		err
	);
	TEST_ASSERT(
		counter_pattern_set_match(&set, "acl_no_match"), "first pattern"
	);
	TEST_ASSERT(
		counter_pattern_set_match(&set, "rule 7"), "second pattern"
	);
	TEST_ASSERT(!counter_pattern_set_match(&set, "rx"), "neither pattern");
	counter_pattern_set_free(&set);

	TEST_ASSERT(
		counter_pattern_set_compile(&set, NULL, 0, err, sizeof(err)) ==
			COUNTER_PATTERN_OK,
		"empty set did not compile"
	);
	TEST_ASSERT(
		!counter_pattern_set_match(&set, "rx"),
		"an empty set must select nothing"
	);
	counter_pattern_set_free(&set);

	counter_pattern_set_match_all(&set);
	TEST_ASSERT(
		counter_pattern_set_match(&set, "anything"),
		"match_all must select everything"
	);
	counter_pattern_set_free(&set);

	return TEST_SUCCESS;
}

static int
test_pattern_set_partial_failure() {
	char toolong[COUNTER_PATTERN_MAX + 32];
	memset(toolong, 'a', sizeof(toolong) - 1);
	toolong[sizeof(toolong) - 1] = '\0';

	const char *patterns[] = {"acl_.*", toolong};

	struct counter_pattern_set set;
	char err[256] = {0};

	TEST_ASSERT(
		counter_pattern_set_compile(
			&set, patterns, 2, err, sizeof(err)
		) == COUNTER_PATTERN_REJECTED,
		"overlong pattern was accepted"
	);
	TEST_ASSERT_STR_CONTAINS(
		err, "longer than", "error should cite the length limit"
	);
	TEST_ASSERT_NULL(
		set.patterns, "a failed compile must leave nothing allocated"
	);

	return TEST_SUCCESS;
}

static int
test_engine_matching() {
	TEST_ASSERT_SUCCESS(
		expect_kind(
			"acl_action_(check_pass|check_miss)",
			COUNTER_PATTERN_ENGINE
		),
		"alternation needs the engine"
	);
	TEST_ASSERT_SUCCESS(
		expect_kind("[a-z]+_drop", COUNTER_PATTERN_ENGINE),
		"character class needs the engine"
	);

	TEST_ASSERT_SUCCESS(
		expect_match(
			"acl_action_(check_pass|check_miss)",
			"acl_action_check_pass",
			true
		),
		"alternation hit"
	);
	TEST_ASSERT_SUCCESS(
		expect_match(
			"acl_action_(check_pass|check_miss)",
			"acl_action_invalid",
			false
		),
		"alternation miss"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("rule [0-9]+", "rule 42", true), "class hit"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("rule [0-9]+", "rule x", false), "class miss"
	);

	return TEST_SUCCESS;
}

static int
test_engine_anchoring() {
	TEST_ASSERT_SUCCESS(
		expect_match("web|db", "web", true), "alternation hit"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("web|db", "web_extra", false),
		"anchors must bind the whole alternation, not one branch"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("web|db", "prefix_db", false),
		"anchors must bind the trailing branch too"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("(?m)web", "other\nweb", false),
		"a multiline flag must not turn the anchors into line anchors"
	);

	return TEST_SUCCESS;
}

static int
test_unterminated_verbose_comment_is_rejected() {
	struct counter_pattern compiled;
	char err[256] = {0};

	bool accepted = counter_pattern_compile(
		&compiled, "(?x)rx # counter", err, sizeof(err)
	);
	if (accepted) {
		counter_pattern_free(&compiled);
	}

	TEST_ASSERT(!accepted, "an unterminated comment was accepted");
	TEST_ASSERT(err[0] != '\0', "rejection did not explain itself");

	TEST_ASSERT_SUCCESS(
		expect_match("(?x)prefix_ rx", "prefix_rx", true),
		"verbose mode must still ignore whitespace"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("(?x)rx # counter\n|tx", "tx", true),
		"a comment the pattern terminates itself must work"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("(?x)rx # counter\n", "rx_extra", false),
		"the end anchor must survive a terminated comment"
	);

	TEST_ASSERT_SUCCESS(
		expect_match("(a|b)#c", "a#c", true), "a literal hash matches"
	);
	TEST_ASSERT_SUCCESS(
		expect_match("(a|b)#c", "a#c_extra", false),
		"a literal hash must not lose the end anchor"
	);

	return TEST_SUCCESS;
}

static int
test_invalid_syntax_is_rejected() {
	struct counter_pattern compiled;
	char err[256] = {0};

	bool accepted = counter_pattern_compile(
		&compiled, "acl_(unclosed", err, sizeof(err)
	);
	if (accepted) {
		counter_pattern_free(&compiled);
	}

	TEST_ASSERT(!accepted, "unbalanced group was accepted");
	TEST_ASSERT(err[0] != '\0', "rejection did not explain itself");

	return TEST_SUCCESS;
}

int
main() {
	log_enable_name("debug");

	size_t tests_count = 0;
	size_t tests_failed = 0;

	struct {
		const char *name;
		int (*run)();
	} tests[] = {
		{"fast_path_kinds", test_fast_path_kinds},
		{"fast_path_matching", test_fast_path_matching},
		{"engine_pattern_count_is_capped",
		 test_engine_pattern_count_is_capped},
		{"overlong_pattern_is_rejected",
		 test_overlong_pattern_is_rejected},
		{"pattern_set", test_pattern_set},
		{"pattern_set_partial_failure", test_pattern_set_partial_failure
		},
		{"pattern_at_the_limit_compiles",
		 test_pattern_at_the_limit_compiles},
		{"engine_matching", test_engine_matching},
		{"engine_anchoring", test_engine_anchoring},
		{"unterminated_verbose_comment_is_rejected",
		 test_unterminated_verbose_comment_is_rejected},
		{"invalid_syntax_is_rejected", test_invalid_syntax_is_rejected},
	};

	for (size_t idx = 0; idx < sizeof(tests) / sizeof(tests[0]); ++idx) {
		++tests_count;
		if (tests[idx].run() != TEST_SUCCESS) {
			++tests_failed;
			LOG(ERROR, "%s failed", tests[idx].name);
		}
	}

	if (tests_failed != 0) {
		LOG(ERROR, "%zu/%zu tests failed", tests_failed, tests_count);
		return 1;
	}

	LOG(INFO, "all %zu tests passed", tests_count);

	return 0;
}
