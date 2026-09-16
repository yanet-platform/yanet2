#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
helper=$script_dir/go-flags.sh

fail() {
	printf 'go-flags test failed: %s\n' "$*" >&2
	exit 1
}

# The Go command makes only the last tags flag effective; the helper then
# appends yanet_asan to that value.
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
goenv=$test_dir/goenv

# Compare the helper's canonical GOFLAGS with the expected parsed fields.
expect_flags() {
	expected=$1
	input=$2
	actual=$(GOENV="$goenv" GOFLAGS="$input" "$helper" yanet_asan)
	[ "$actual" = "$expected" ] || fail "GOFLAGS=$input: got '$actual', want '$expected'"
}

expect_invalid() {
	input=$1
	if output=$(GOENV="$goenv" GOFLAGS="$input" "$helper" yanet_asan 2>/dev/null); then
		fail "invalid GOFLAGS=$input produced '$output'"
	fi
}

expect_flags '-tags=yanet_asan' ''
expect_flags '-mod=mod -tags=yanet_asan' '-mod=mod'
expect_flags '-mod=mod -tags=feature,other,yanet_asan' '-tags=feature,other -mod=mod'
expect_flags '-mod=mod -tags=feature,other,yanet_asan' '--tags=feature,other -mod=mod'
expect_flags '-mod=mod -tags=another,yanet_asan' '-tags=feature,other -mod=mod -tags=another'
expect_flags '-mod=mod -tags=another,yanet_asan' '--tags=feature,other -mod=mod --tags=another'
expect_flags '-mod=mod -tags=feature,yanet_asan' '-tags=feature,yanet_asan -mod=mod'
expect_flags '-trimpath -tags=yanet_asan' '-trimpath'
expect_flags "'-modfile=/tmp/a b.mod' -tags=yanet_asan" '"-modfile=/tmp/a b.mod"'
expect_invalid '-tags feature,other'
expect_invalid '--tags feature,other'

# Exercise Go's parser with an inherited flag and a value containing spaces.
module_dir=$test_dir/module
mkdir "$module_dir"
cat > "$module_dir/go.mod" <<'EOF'
module example.com/go-flags-test

go 1.24.13
EOF
cp "$module_dir/go.mod" "$module_dir/alternate module.mod"
cat > "$module_dir/obsolete.go" <<'EOF'
//go:build obsolete && yanet_asan

package probe
EOF
cat > "$module_dir/persisted.go" <<'EOF'
//go:build persisted && yanet_asan

package probe
EOF
cat > "$module_dir/selected.go" <<'EOF'
//go:build other && yanet_asan

package probe
EOF

# Persisted GOFLAGS must be read through Go without touching the user's config.
GOENV="$goenv" env -u GOFLAGS go env -w GOFLAGS='-mod=mod -tags=obsolete -tags=persisted'
persisted=$(GOENV="$goenv" env -u GOFLAGS "$helper" yanet_asan)
expected='-mod=mod -tags=persisted,yanet_asan'
[ "$persisted" = "$expected" ] || fail "persisted GOFLAGS: got '$persisted', want '$expected'"
persisted_listed=$(cd "$module_dir" && GOENV="$goenv" GOFLAGS="$persisted" go list -f '{{join .GoFiles ","}}' .) || fail "Go rejected persisted '$persisted'"
[ "$persisted_listed" = "persisted.go" ] || fail "Go did not retain persisted flags: got '$persisted_listed'"

overridden=$(GOENV="$goenv" GOFLAGS='-mod=mod -tags=override' "$helper" yanet_asan)
expected='-mod=mod -tags=override,yanet_asan'
[ "$overridden" = "$expected" ] || fail "environment GOFLAGS: got '$overridden', want '$expected'"

input="\"-modfile=$module_dir/alternate module.mod\" --tags=obsolete -tags=other"
merged=$(GOENV="$goenv" GOFLAGS="$input" "$helper" yanet_asan)
expected="'-modfile=$module_dir/alternate module.mod' -tags=other,yanet_asan"
[ "$merged" = "$expected" ] || fail "combined GOFLAGS: got '$merged', want '$expected'"
listed=$(cd "$module_dir" && GOENV="$goenv" GOFLAGS="$merged" go list -f '{{join .GoFiles ","}}' .) || fail "Go rejected '$merged'"
[ "$listed" = "selected.go" ] || fail "Go retained an obsolete tag: got '$listed'"

# Reapplying the helper output must be idempotent rather than recursing or
# dropping any effective flags.
rerendered=$(GOENV="$goenv" GOFLAGS="$merged" "$helper" yanet_asan)
[ "$rerendered" = "$merged" ] || fail "reapplied GOFLAGS: got '$rerendered', want '$merged'"

printf 'go-flags cases passed\n'
