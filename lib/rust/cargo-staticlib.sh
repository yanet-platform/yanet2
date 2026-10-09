#!/bin/sh
# Builds one staticlib crate of a Rust workspace for meson.
#
# Copies the archive to the meson output and rewrites the cargo dep-info so
# ninja rebuilds it when any Rust source or bound C header changes. With a
# header name, also copies that header from the crate's build-script output
# directory, where its build script generated it, next to the archive.
#
# Usage: cargo-staticlib.sh CARGO MANIFEST TARGET_DIR PACKAGE OUTPUT DEPFILE [HEADER]
set -eu

cargo=$1
manifest=$2
target_dir=$3
package=$4
output=$5
depfile=$6
header=${7:-}

lib=$(printf '%s' "$package" | tr '-' '_')
messages="$target_dir/$package.messages.json"

mkdir -p "$target_dir"

"$cargo" build --release --locked --quiet \
	--message-format=json-render-diagnostics \
	--manifest-path "$manifest" \
	--target-dir "$target_dir" \
	--package "$package" >"$messages"

cp "$target_dir/release/lib$lib.a" "$output"
sed "1s|^[^:]*:|$output:|" "$target_dir/release/lib$lib.d" >"$depfile"

if [ -n "$header" ]; then
	out_dir=$(grep '"reason":"build-script-executed"' "$messages" |
		grep "/$package#" |
		sed -n 's/.*"out_dir":"\([^"]*\)".*/\1/p' |
		tail -n 1)
	if [ -z "$out_dir" ]; then
		echo "cargo-staticlib.sh: no build-script output for $package" >&2
		exit 1
	fi
	cp "$out_dir/$header" "$(dirname "$output")/$header"
fi
