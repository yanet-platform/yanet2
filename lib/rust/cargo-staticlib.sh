#!/bin/sh
# Builds one staticlib crate of the dataplane Rust workspace for meson.
#
# Copies the archive to the meson output and rewrites the cargo dep-info so
# ninja rebuilds it when any Rust source or bound C header changes.
#
# Usage: cargo-staticlib.sh CARGO MANIFEST TARGET_DIR PACKAGE OUTPUT DEPFILE
set -eu

cargo=$1
manifest=$2
target_dir=$3
package=$4
output=$5
depfile=$6

lib=$(printf '%s' "$package" | tr '-' '_')

"$cargo" build --release --locked --quiet \
	--manifest-path "$manifest" \
	--target-dir "$target_dir" \
	--package "$package"

cp "$target_dir/release/lib$lib.a" "$output"
sed "1s|^[^:]*:|$output:|" "$target_dir/release/lib$lib.d" >"$depfile"
