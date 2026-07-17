#!/bin/sh

# commitlint enforces the "<type>(<scope>): <brief>" subject convention on
# commit messages and PR titles.
#
# The convention is documented in CLAUDE.md under "Commits & PRs": type is
# one of a closed set, scope is mandatory and lowercase, an optional "!"
# marks a breaking change, and the brief must not start with an uppercase
# letter and carries no trailing period. Enforcing it mechanically keeps
# history greppable by scope and type without relying on reviewers to catch
# drift.
#
# The classification below relies on POSIX character ranges ([a-z], [A-Z],
# [[:space:]]) rather than Unicode properties, so it is forced to the C
# locale: an accented uppercase letter starting a brief is not caught, which
# matches the repo's all-ASCII commit history.
export LC_ALL=C

allowed_types="feat fix refactor perf chore docs test build ci style"

usage() {
	printf 'usage: %s --file <path> | --subject <text> | --range <a>..<b>\n' "$0" >&2
}

# validate_header checks the "type(scope)" or "type(scope)!" prefix of a
# subject, before the ": " separator, printing a diagnostic and returning 1
# on any violation.
validate_header() {
	header=$1

	if ! printf '%s' "$header" | grep -qE '^[a-z]+(\([^)]*\))?!?$'; then
		printf 'header "%s" must look like "type(scope)", optionally followed by "!"\n' "$header"
		return 1
	fi

	commit_type=$(printf '%s' "$header" | sed -E 's/^([a-z]+)(\(([^)]*)\))?!?$/\1/')
	scope_raw=$(printf '%s' "$header" | sed -E 's/^([a-z]+)(\(([^)]*)\))?!?$/\3/')

	case "$header" in
	*\(*) has_scope=1 ;;
	*) has_scope=0 ;;
	esac

	case " $allowed_types " in
	*" $commit_type "*) ;;
	*)
		printf 'unknown type "%s", must be one of feat, fix, refactor, perf, chore, docs, test, build, ci, style\n' "$commit_type"
		return 1
		;;
	esac

	if [ "$has_scope" -eq 0 ]; then
		printf 'scope is mandatory, expected "%s" with a scope, e.g. "%s(scope)"\n' "$header" "$commit_type"
		return 1
	fi

	if [ -z "$scope_raw" ]; then
		printf 'scope must not be empty\n'
		return 1
	fi

	if ! printf '%s' "$scope_raw" | grep -qE '^[a-z0-9][a-z0-9._/-]*(,[a-z0-9][a-z0-9._/-]*)*$'; then
		printf 'scope "%s" must match [a-z0-9][a-z0-9._/-]*, comma-separated\n' "$scope_raw"
		return 1
	fi

	return 0
}

# validate checks subject against the "<type>(<scope>): <brief>" convention,
# printing nothing and returning 0 for a conforming subject, or printing a
# diagnostic naming the offending subject and the rule it broke and
# returning 1 otherwise.
#
# Merge commits and reverts are skipped without validation, since they carry
# no type/scope by design. Autosquash markers (fixup!/squash!/amend!) are
# NOT skipped here: run_file skips them before calling validate, since
# git commit --fixup is a legitimate local workflow cleaned up later by an
# interactive rebase, but --subject and --range (the CI callers of validate)
# must reject a marker that is still present at PR time, since it can only
# mean the commit either gets squashed away or, on a repo that also allows
# plain "Rebase and merge", rides onto main unsquashed.
validate() {
	subject=$1

	trimmed=$(printf '%s' "$subject" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')
	if [ -z "$trimmed" ]; then
		printf 'subject is empty\n'
		return 1
	fi

	case "$subject" in
	"Merge "* | 'Revert "'*)
		return 0
		;;
	esac

	# Strip a trailing GitHub PR-number suffix before the separator check, so
	# a squash-merged subject is validated the same as its source commit.
	subject=$(printf '%s' "$subject" | sed -E 's/[[:space:]]\(#[0-9]+\)$//')

	case "$subject" in
	*": "*)
		header=${subject%%: *}
		brief=${subject#*: }
		;;
	*)
		printf 'subject "%s" must contain a ": " separator after "type(scope)"\n' "$subject"
		return 1
		;;
	esac

	if ! header_error=$(validate_header "$header"); then
		printf 'subject "%s": %s\n' "$subject" "$header_error"
		return 1
	fi

	if [ -z "$brief" ]; then
		printf 'subject "%s": brief must not be empty\n' "$subject"
		return 1
	fi

	case "$brief" in
	[A-Z]*)
		printf 'subject "%s": brief "%s" must not start with an uppercase letter\n' "$subject" "$brief"
		return 1
		;;
	esac

	case "$brief" in
	*.)
		printf 'subject "%s": brief "%s" must not end with a trailing period\n' "$subject" "$brief"
		return 1
		;;
	esac

	return 0
}

# first_subject_line prints the first non-empty, non-comment line of a
# commit message file, or nothing if there is none.
#
# The blank check trims both sides, since a whitespace-only line is skipped
# regardless of indentation. The comment check tests only for a "#" at
# column 0, matching git's default --cleanup=strip, which does not treat an
# indented "#" as a comment. The printed line is only trimmed on the
# trailing side; trimming the leading side here would make the hook accept
# a subject that CI's --range mode, which reads the raw subject, still
# rejects.
first_subject_line() {
	file=$1

	while IFS= read -r line || [ -n "$line" ]; do
		trimmed=$(printf '%s' "$line" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')
		if [ -z "$trimmed" ]; then
			continue
		fi
		case "$line" in
		"#"*) continue ;;
		esac

		printf '%s\n' "$line" | sed -E 's/[[:space:]]+$//'
		return 0
	done <"$file"
}

# run_file validates the subject read from a commit message file, exiting
# non-zero and printing a diagnostic on failure.
#
# An autosquash marker (fixup!/squash!/amend!) is accepted here without
# validation, since git commit --fixup=<ref> is a legitimate local workflow
# whose subject is squashed away later by git rebase -i --autosquash.
run_file() {
	path=$1

	if [ ! -r "$path" ]; then
		printf 'failed to read commit message file: %s\n' "$path" >&2
		exit 1
	fi

	subject=$(first_subject_line "$path")
	if [ -z "$subject" ]; then
		printf 'commit message has no subject line\n' >&2
		exit 1
	fi

	case "$subject" in
	"fixup! "* | "squash! "* | "amend! "*)
		return 0
		;;
	esac

	if ! error_message=$(validate "$subject"); then
		printf '%s\n' "$error_message" >&2
		exit 1
	fi
}

# run_subject validates a single literal subject, exiting non-zero and
# printing a diagnostic on failure.
run_subject() {
	subject=$1

	if ! error_message=$(validate "$subject"); then
		printf '%s\n' "$error_message" >&2
		exit 1
	fi
}

# run_range validates every non-merge commit subject in commit_range,
# reporting every failure rather than stopping at the first one.
run_range() {
	commit_range=$1

	subjects_file=$(mktemp)
	stderr_file=$(mktemp)
	trap 'rm -f "$subjects_file" "$stderr_file"' EXIT

	if ! git log --no-merges --format=%s "$commit_range" >"$subjects_file" 2>"$stderr_file"; then
		printf 'failed to run git log: %s\n' "$(cat "$stderr_file")" >&2
		exit 1
	fi

	# A blank line here is not noise: git collapses %s to a single line per
	# commit, so a blank line unambiguously means an empty-subject commit
	# and must be diagnosed the same as `--subject ''`, not skipped.
	failed=0
	while IFS= read -r subject || [ -n "$subject" ]; do
		if ! error_message=$(validate "$subject"); then
			printf '%s\n' "$error_message"
			failed=1
		fi
	done <"$subjects_file"

	if [ "$failed" -eq 1 ]; then
		exit 1
	fi
}

file_path=""
subject_arg=""
range_arg=""
has_file=0
has_subject=0
has_range=0

while [ $# -gt 0 ]; do
	case "$1" in
	--file)
		file_path=$2
		has_file=1
		shift 2
		;;
	--subject)
		subject_arg=$2
		has_subject=1
		shift 2
		;;
	--range)
		range_arg=$2
		has_range=1
		shift 2
		;;
	*)
		printf 'unknown argument: %s\n' "$1" >&2
		usage
		exit 2
		;;
	esac
done

if [ "$((has_file + has_subject + has_range))" -ne 1 ]; then
	printf 'exactly one of --file, --subject, --range must be given\n' >&2
	usage
	exit 2
fi

if [ "$has_file" -eq 1 ]; then
	run_file "$file_path"
elif [ "$has_subject" -eq 1 ]; then
	run_subject "$subject_arg"
else
	run_range "$range_arg"
fi
