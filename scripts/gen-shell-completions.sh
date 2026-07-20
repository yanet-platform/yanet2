#!/bin/sh
set -eu

install_list="$1"
bash_dir="$2"
zsh_dir="$3"

mkdir -p "$bash_dir" "$zsh_dir"

while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
	usr/bin/yanet-cli*)
		bin=$(basename "$line")
		fn=_clap_dynamic_completer_$(printf '%s' "$bin" | tr -c '[:alnum:]' '_')
		printf 'source <(COMPLETE=bash %s)\n' "$bin" >"$bash_dir/$bin"
		# Tail-call the sourced completer so the autoloaded #compdef file answers the first Tab.
		printf '#compdef %s\nsource <(COMPLETE=zsh %s)\n%s "$@"\n' "$bin" "$bin" "$fn" >"$zsh_dir/_$bin"
		;;
	esac
done <"$install_list"
