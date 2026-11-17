#!/usr/bin/env bash
# Runs the checks CI runs, for the layers a change touches, and prints only
# what fails.
#
# The change is the checkout's state (commits, staged, unstaged and
# untracked files) against its merge base with origin/main, so run it from
# the task worktree. A layer stops at its first failing step and the other
# layers still run; a check whose tool is not installed is listed as
# skipped and left to CI.

set -u

usage() {
	echo "usage: scripts/gate.sh [--base <rev>] [--list] [--verbose]" >&2
	exit 2
}

base=""
list=0
verbose=0
while [ $# -gt 0 ]; do
	case "$1" in
	--base)
		[ $# -ge 2 ] || usage
		base=$2
		shift 2
		;;
	--list)
		list=1
		shift
		;;
	--verbose)
		verbose=1
		shift
		;;
	*) usage ;;
	esac
done

root=$(git rev-parse --show-toplevel) || exit 2
cd "$root" || exit 2
if [ -z "$base" ]; then
	base=$(git merge-base HEAD origin/main 2>/dev/null) || {
		echo "gate: no merge base with origin/main; pass --base" >&2
		exit 2
	}
fi
git rev-parse --quiet --verify "$base^{commit}" >/dev/null || {
	echo "gate: $base is not a commit" >&2
	exit 2
}

changed=$({
	git diff --name-only --no-renames "$base"
	git ls-files --others --exclude-standard
} | sort -u)

# Changed paths matching an extended regex, and the subset still on disk.
matching() { printf '%s\n' "$changed" | grep -E "$1"; }
present() { matching "$1" | while IFS= read -r f; do [ -f "$f" ] && printf '%s\n' "$f"; done; }

# The step log and any copy a check builds outside the checkout.
scratch=$(mktemp -d) || exit 2
trap 'rm -rf "$scratch"' EXIT
log=$scratch/log
failed=0
passed=()
skipped=()

# Runs one named check; a failure prints the tail of its output.
step() {
	local name=$1 rc
	shift
	if [ "$list" = 1 ]; then
		echo "$name: $*"
		return 0
	fi
	if [ "$verbose" = 1 ]; then
		echo "== $name: $*"
		"$@"
	else
		"$@" >"$log" 2>&1
	fi
	rc=$?
	if [ "$rc" -ne 0 ]; then
		echo "FAIL $name: $*"
		[ "$verbose" = 1 ] || tail -n 60 "$log"
		failed=$((failed + 1))
		return 1
	fi
	passed+=("$name")
}

fail() {
	echo "FAIL $1: $2"
	failed=$((failed + 1))
}

gofmt_clean() {
	local out
	out=$(gofmt -l "$@") || return 1
	[ -z "$out" ] || {
		echo "not gofmt-formatted:"
		echo "$out"
		return 1
	}
}

gate_c() {
	local files want have
	files=$(present '\.[ch]$' | grep -v '^subprojects/')
	if [ -n "$files" ]; then
		want=$(sed -n 's/^ARG VERSION=\([0-9]*\).*/\1/p' .github/actions/clang-format/Dockerfile)
		have=$(clang-format --version 2>/dev/null | sed -n 's/.*version \([0-9]*\)\..*/\1/p')
		if [ "$have" != "$want" ]; then
			skipped+=("clang-format (local ${have:-none}, CI $want)")
		else
			# shellcheck disable=SC2086
			step clang-format clang-format --dry-run --Werror --style=file $files || return 1
		fi
	fi
	if [ ! -d build ] || [ -L build ]; then
		fail meson "C changes need this checkout's own build/; run 'meson setup build' (AGENTS.md, Worktrees)"
		return 1
	fi
	step meson-compile meson compile -C build || return 1
	step meson-test meson test -C build --print-errorlogs
}

gate_go() {
	local files pkgs cppflags proto
	if [ ! -f subprojects/dpdk/meson.build ] || [ ! -d build ]; then
		fail go "cgo packages typecheck against the DPDK sources and a configured build/; run 'git submodule update --init && meson setup build'"
		return 1
	fi
	proto=$(git ls-files '*.proto' | head -n 1)
	if matching '\.proto$' >/dev/null || [ ! -f "${proto%.proto}.pb.go" ]; then
		step proto-go make --no-print-directory proto-go || return 1
	fi
	files=$(present '\.go$' | grep -v '\.pb\.go$')
	if [ -n "$files" ]; then
		# shellcheck disable=SC2086
		step gofmt gofmt_clean $files || return 1
	fi
	step stylelint go run ./lint/style/cmd/stylelint/ || return 1
	step go-vet go vet ./... || return 1

	# Packages that hold changed Go files or link changed C; C or build
	# changes outside a module and module requirement changes reach every
	# package. The QEMU suite is left to make test-functional.
	if matching '(^go\.(mod|sum)$|\.proto$|^scripts/go_c(deps|go_flags)\.py$)' >/dev/null || matching '(\.[ch]$|(^|/)meson\.build$|^subprojects/)' | grep -qv '^modules/'; then
		pkgs=./...
		[ "$list" = 1 ] || pkgs=$(go list ./... | grep -v '/tests/functional/main$')
	else
		pkgs=$({
			matching '\.go$' | xargs -r -n 1 dirname | sort -u | while IFS= read -r d; do
				go list "./$d" >/dev/null 2>&1 && echo "./$d"
			done
			matching '^modules/[^/]+/.*(\.[ch]|meson\.build)$' | cut -d / -f 1-2 | sed 's|^|./|; s|$|/...|'
		} | grep -v '^\./tests/functional/main$' | sort -u)
	fi
	[ -n "$pkgs" ] || return 0

	# A package that links cgo needs this checkout's own build/: preparing it
	# writes the native archives and the cgo dependency sources the link uses.
	# shellcheck disable=SC2086
	if [ "$list" = 1 ] || [ -n "$(go list -deps -test -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' $pkgs 2>/dev/null | head -n 1)" ]; then
		if [ -L build ]; then
			fail go-test "packages that link cgo need this checkout's own build/, not a link to another one; run 'meson setup build' (AGENTS.md, Worktrees)"
			return 1
		fi
		step go-test-prepare meson compile -C build go-test-prepare || return 1
		if [ "$list" != 1 ] && [ ! -f build/yanet_cgo_cppflags ]; then
			fail go-test "build/ has no yanet_cgo_cppflags, so cgo would build with the default cache-line size; remove it and run 'meson setup build'"
			return 1
		fi
	fi
	cppflags=$(cat build/yanet_cgo_cppflags 2>/dev/null)
	# shellcheck disable=SC2086
	step go-test env CGO_CPPFLAGS="${CGO_CPPFLAGS:-} $cppflags" go test -count=1 $pkgs || return 1
	if command -v golangci-lint >/dev/null; then
		step golangci-lint golangci-lint run ./...
	else
		skipped+=("golangci-lint (not installed)")
	fi
}

gate_rust() {
	local toolchain
	toolchain=$(sed -n 's/^ *toolchain: *\(nightly-[0-9-]*\).*/\1/p' .github/workflows/rust-check.yml | head -n 1)
	step cargo-fmt cargo "+$toolchain" fmt --all -- --check || return 1
	step clippy env CARGO_BUILD_WARNINGS=deny cargo clippy --workspace --all-targets --locked --keep-going || return 1
	step cargo-test cargo test --workspace --locked
}

# The Rust SDK workspace (bindings/rust), as rust-sdk.yml checks it on this
# machine's architecture.
#
# The bindings crate reads the C flags of this checkout's own build/ and the
# DPDK headers, and cargo finds the workspace's config from the working
# directory, so every step runs there.
gate_sdk() {
	local toolchain target sdk=(env -C bindings/rust) packet=$scratch/yanet-packet
	if [ ! -f subprojects/dpdk/meson.build ] || [ ! -d build ] || [ -L build ]; then
		fail sdk "the SDK bindings read the DPDK sources and this checkout's own build/; run 'git submodule update --init && meson setup build' (AGENTS.md, Worktrees)"
		return 1
	fi
	toolchain=$(sed -n 's/^ *toolchain: *\(nightly-[0-9-]*\).*/\1/p' .github/workflows/rust-sdk.yml | head -n 1)
	step sdk-fmt "${sdk[@]}" cargo "+$toolchain" fmt -- --check || return 1
	if command -v cargo-shear >/dev/null; then
		step sdk-shear "${sdk[@]}" cargo shear --deny-warnings --locked || return 1
	else
		skipped+=("cargo-shear (not installed)")
	fi
	step sdk-clippy "${sdk[@]}" CARGO_BUILD_WARNINGS=deny cargo clippy --workspace --all-targets --locked --keep-going || return 1
	step sdk-clippy-plugin "${sdk[@]}" CARGO_BUILD_WARNINGS=deny cargo clippy --profile plugin -p yanet-sdk-fixture --features plugin --examples --locked --keep-going || return 1
	step sdk-clippy-deps "${sdk[@]}" cargo clippy --workspace --lib --locked -- -D unused_crate_dependencies || return 1
	step sdk-doc "${sdk[@]}" RUSTDOCFLAGS='-D warnings' cargo doc --workspace --no-deps --locked --features yanet-sdk-testing/cp || return 1
	target="$(uname -m)-unknown-none"
	if rustup target list --installed 2>/dev/null | grep -qx "$target"; then
		step sdk-no-std "${sdk[@]}" cargo check --locked --target "$target" -p yanet-shm -p yanet-sys -p yanet-sdk -p yanet-sdk-plugin -p yanet-packet || return 1
	else
		skipped+=("sdk-no-std ($target not installed)")
	fi
	step sdk-test "${sdk[@]}" PROPTEST_CASES=200 PROPTEST_RNG_SEED=19567271 cargo test --workspace --locked || return 1
	step sdk-test-release "${sdk[@]}" cargo test -p yanet-sys --release --locked || return 1
	step sdk-bench "${sdk[@]}" cargo test -p yanet-sys --bench dp --bench lookup --locked || return 1
	step sdk-bench-release "${sdk[@]}" cargo test -p yanet-sys --release --bench lookup --locked || return 1

	# The packet crate must build as its own repository would, from a copy
	# outside the workspace.
	matching '^bindings/rust/yanet-packet/' >/dev/null || return 0
	if [ "$list" != 1 ] && ! cp -r bindings/rust/yanet-packet "$packet"; then
		fail sdk-packet "cannot copy the packet crate to $packet"
		return 1
	fi
	step sdk-packet-test env -C "$packet" cargo test || return 1
	if rustup target list --installed 2>/dev/null | grep -qx "$target"; then
		step sdk-packet-no-std env -C "$packet" cargo check --target "$target"
	else
		skipped+=("sdk-packet-no-std ($target not installed)")
	fi
}

gate_web() {
	if [ ! -d node_modules ]; then
		fail web "no node_modules/; run 'npm ci' at the repository root"
		return 1
	fi
	step tsc npx tsc --noEmit -p web/tsconfig.json || return 1
	step vitest npm run test -w web || return 1
	step web-build npm run build -w web
}

c=0
matching '(\.[ch]$|(^|/)meson\.build$|^meson_options\.txt$|^ubsan-ignorelist\.txt$|^subprojects/|^scripts/(go_c(deps|go_flags)|check_link_deps)\.py$)' >/dev/null && c=1
[ "$c" = 1 ] && gate_c
if [ "$c" = 1 ] || matching '(\.go$|^go\.(mod|sum)$|\.proto$)' >/dev/null; then gate_go; fi
# The SDK workspace is not a member of the root one; as in CI, only its own
# layer checks it.
matching '(\.rs$|(^|/)Cargo\.(toml|lock)$|^\.rustfmt\.toml$|^\.cargo/|\.proto$)' | grep -qv '^bindings/rust/' && gate_rust
matching '(^bindings/rust/|^\.rustfmt\.toml$|^\.cargo/)' >/dev/null && gate_sdk
matching '(^|/)web/|^package(-lock)?\.json$' >/dev/null && gate_web
matching '\.proto$' >/dev/null && step proto-lint make --no-print-directory proto-lint
[ -n "$(git rev-list "$base"..HEAD)" ] && step commitlint lint/commit/commitlint.sh --range "$base..HEAD"

[ "$list" = 1 ] && exit 0
[ ${#skipped[@]} -eq 0 ] || echo "skipped: ${skipped[*]}"
if [ "$failed" -ne 0 ]; then
	echo "gate: $failed failed"
	exit 1
fi
echo "gate: ok (${passed[*]:-nothing to check})"
