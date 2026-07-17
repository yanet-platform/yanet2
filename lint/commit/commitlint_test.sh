#!/bin/sh

# commitlint_test.sh runs commitlint.sh against a table of subjects with
# known verdicts and fails if any of them gets the wrong one.
#
# Each case here mirrors one of the rules documented in CLAUDE.md under
# "Commits & PRs": every accepted type, the mandatory scope, comma-separated
# multi-scope, the breaking-change marker, the GitHub PR-number suffix, and
# every rejection rule down to the exact diagnostic it must report.

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
commitlint="$script_dir/commitlint.sh"

failures=0

expect_pass() {
	subject=$1
	if ! "$commitlint" --subject "$subject" >/dev/null 2>&1; then
		printf 'FAIL (want pass): %s\n' "$subject"
		failures=$((failures + 1))
	fi
}

expect_fail() {
	subject=$1
	if "$commitlint" --subject "$subject" >/dev/null 2>&1; then
		printf 'FAIL (want fail): %s\n' "$subject"
		failures=$((failures + 1))
	fi
}

# expect_fail_message additionally checks that the diagnostic names the
# rule that was broken, so a case can't pass for the wrong reason.
expect_fail_message() {
	subject=$1
	needle=$2

	output=$("$commitlint" --subject "$subject" 2>&1)
	status=$?
	if [ "$status" -eq 0 ]; then
		printf 'FAIL (want fail): %s\n' "$subject"
		failures=$((failures + 1))
		return
	fi

	case "$output" in
	*"$needle"*) ;;
	*)
		printf 'FAIL (want message containing %s): %s -> %s\n' "$needle" "$subject" "$output"
		failures=$((failures + 1))
		;;
	esac
}

expect_pass "feat(route): add support for foo"
expect_pass "fix(acl): correct the icmpv6 target slice"
expect_pass "refactor(pdump): collapse duplicated printer bodies"
expect_pass "perf(dataplane): skip demux on a single chain"
expect_pass "chore(cli): bump the release version"
expect_pass "docs(gateway): describe the registration flow"
expect_pass "test(functest): add unfiltered socket capture for unsolicited traffic"
expect_pass "build(cli): pin the protoc-gen-go version"
expect_pass "ci(github): cache the cargo registry"
expect_pass "style(acl): reflow the comment block"

expect_pass "refactor(acl,fwstate): share the chain lookup"

expect_pass "fix(dataplane_ut): stabilize the bench harness"
expect_pass "fix(cli/acl): correct the mask parser"
expect_pass "feat(operators/decap): add the readiness probe"
expect_pass "fix(bird-adapter): reconnect on stream drop"
expect_pass "fix(route-mpls): correct the label pop"
expect_pass "refactor(acl,fwstate): share the chain lookup"
expect_pass "chore(web): 8 columns for modules cards"

expect_pass "feat(route)!: change the wire format"
expect_pass "feat(acl,fwstate)!: x"

expect_pass "fix(functional): stabilize VM snapshot fallback in CI (#1400)"
expect_pass "refactor(acl): simplify (again)"
expect_pass "fix(acl): handle the (#) marker"
expect_pass "refactor(pdump): collapse printers (#1461)"

expect_fail_message "feature(route): add support for foo" "unknown type"
expect_fail_message "ai(x): y" "unknown type"
expect_fail_message "feat: add thing" "scope is mandatory"
expect_fail_message "fix(Route): correct the label pop" "scope"
expect_fail_message "fix(route): Correct the label pop" "uppercase letter"
expect_fail_message "feat(bench): Go-driven dataplane benchmarks" "uppercase letter"
expect_fail_message "fix(route): correct the label pop." "trailing period"
expect_fail_message "fix(route): " "brief must not be empty"
expect_fail_message "fix(route):correct the label pop" ': " separator'

expect_pass "Merge pull request #1234 from user/branch"
expect_pass 'Revert "fix(route): correct the label pop"'

# Autosquash markers are only exempted for the commit-msg hook (--file); CI
# (--subject, --range) must reject them so a fixup commit cannot ride onto
# main unsquashed via a plain "Rebase and merge".
expect_fail "fixup! fix(route): correct the label pop"
expect_fail "squash! fix(route): correct the label pop"
expect_fail "amend! fix(route): correct the label pop"

expect_fail "xfeat(a): b"
expect_fail "prefix feat(a): b"
expect_fail "feat(): x"
expect_fail "feat(a, b): x"
expect_fail "feat!(a): x"
expect_fail "feat(a)!!: x"
expect_fail "no separator here"

expect_fail "feat(,a): x"
expect_fail "feat(a,,b): x"
expect_fail "feat(a,): x"
expect_fail "refactor(acl,fwstate,): x"

lead_file=$(mktemp)
printf '\n# Please enter the commit message for your changes.\n' >"$lead_file"
printf 'feat(route): add support for foo\n\n' >>"$lead_file"
printf '# On branch main\n# Changes to be committed:\n' >>"$lead_file"
if ! "$commitlint" --file "$lead_file" >/dev/null 2>&1; then
	printf 'FAIL (want pass, comments/blank lines should be skipped): --file %s\n' "$lead_file"
	failures=$((failures + 1))
fi
rm -f "$lead_file"

# The commit-msg hook must still accept an autosquash marker, since
# git commit --fixup=<ref> is a legitimate local workflow.
fixup_file=$(mktemp)
printf 'fixup! fix(route): correct the label pop\n' >"$fixup_file"
if ! "$commitlint" --file "$fixup_file" >/dev/null 2>&1; then
	printf 'FAIL (want pass, hook must accept fixup!): --file %s\n' "$fixup_file"
	failures=$((failures + 1))
fi
rm -f "$fixup_file"

empty_file=$(mktemp)
printf '\n# only comments\n\n' >"$empty_file"
if "$commitlint" --file "$empty_file" >/dev/null 2>&1; then
	printf 'FAIL (want fail, no subject line found): --file %s\n' "$empty_file"
	failures=$((failures + 1))
fi
rm -f "$empty_file"

# Leading whitespace on a subject must survive into validate() and be
# rejected there, matching git's own trailing-only --cleanup=strip.
leading_file=$(mktemp)
printf '  fix(route): leading spaces\n' >"$leading_file"
if "$commitlint" --file "$leading_file" >/dev/null 2>&1; then
	printf 'FAIL (want fail, leading whitespace must not be stripped): --file %s\n' "$leading_file"
	failures=$((failures + 1))
fi
rm -f "$leading_file"

# Trailing whitespace on a subject must be stripped, matching git's
# --cleanup=strip.
trailing_file=$(mktemp)
printf 'fix(route): trailing spaces   \n' >"$trailing_file"
if ! "$commitlint" --file "$trailing_file" >/dev/null 2>&1; then
	printf 'FAIL (want pass, trailing whitespace must be stripped): --file %s\n' "$trailing_file"
	failures=$((failures + 1))
fi
rm -f "$trailing_file"

# Only a "#" at column 0 is a git comment; an indented "#" is the subject
# and must be rejected, matching git's --cleanup=strip.
indented_comment_file=$(mktemp)
printf '  # indented\n' >"$indented_comment_file"
if "$commitlint" --file "$indented_comment_file" >/dev/null 2>&1; then
	printf 'FAIL (want fail, indented "#" line is the subject): --file %s\n' "$indented_comment_file"
	failures=$((failures + 1))
fi
rm -f "$indented_comment_file"

# run_range must not let git log's blank line for an empty-subject commit
# slip through as nothing to validate.
#
# A hermetic scratch repo pins one conforming commit as the lower bound of
# a known-good range (must pass) and appends an empty-subject commit after
# it, so a range that reaches the tip must fail with an "empty" diagnostic
# while the range excluding it still passes.
range_repo=$(mktemp -d)
trap 'rm -rf "$range_repo"' EXIT

git -C "$range_repo" init -q
git -C "$range_repo" config user.email test@example.com
git -C "$range_repo" config user.name test
git -C "$range_repo" commit -q --allow-empty -m 'feat(range): add the base commit'
base=$(git -C "$range_repo" rev-parse HEAD)
git -C "$range_repo" commit -q --allow-empty -m 'fix(range): add a conforming commit'
good_head=$(git -C "$range_repo" rev-parse HEAD)
git -C "$range_repo" commit -q --allow-empty --allow-empty-message -m ''

if ! (cd "$range_repo" && "$commitlint" --range "$base..$good_head") >/dev/null 2>&1; then
	printf 'FAIL (want pass, range of conforming commits): %s..%s\n' "$base" "$good_head"
	failures=$((failures + 1))
fi

if range_output=$(cd "$range_repo" && "$commitlint" --range "$base..HEAD" 2>&1); then
	printf 'FAIL (want fail, range hides an empty-subject commit)\n'
	failures=$((failures + 1))
else
	case "$range_output" in
	*"subject is empty"*) ;;
	*)
		printf 'FAIL (want message containing subject is empty): %s\n' "$range_output"
		failures=$((failures + 1))
		;;
	esac
fi

if [ "$failures" -ne 0 ]; then
	printf '%d case(s) failed\n' "$failures"
	exit 1
fi

printf 'all commitlint cases passed\n'
