#!/bin/bash
# Computes the next YANET release version from the commits on origin/main and
# keeps every component version in step with it. See ../SKILL.md for the flow.
set -euo pipefail

semver='(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'
bump_subject='chore(release): bump version to'

usage() {
	cat >&2 <<'EOF'
usage: bump.sh status
       bump.sh next [--to <rev>] [--expect X.Y.Z] [major|minor|patch|X.Y.Z]
       bump.sh apply X.Y.Z
       bump.sh check X.Y.Z
EOF
	exit 2
}

fail() {
	echo "bump.sh: $*" >&2
	exit 1
}

# Prints "<version> <commit>" for every vX.Y.Z tag on origin, lowest version first.
# Local tags are ignored because a clone can keep tags that origin no longer has.
origin_tags() {
	git ls-remote --tags origin 'refs/tags/v*' | awk '
		{
			ref = $2
			sub("^refs/tags/", "", ref)
			peeled = sub(/\^\{\}$/, "", ref)
			if (ref !~ /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/) next
			if (peeled || !(ref in sha)) sha[ref] = $1
		}
		END { for (ref in sha) print substr(ref, 2), sha[ref] }' | sort -k1,1V
}

bump() {
	local major minor patch
	IFS=. read -r major minor patch <<<"$1"
	case $2 in
	major) echo "$((major + 1)).0.0" ;;
	minor) echo "$major.$((minor + 1)).0" ;;
	patch) echo "$major.$minor.$((patch + 1))" ;;
	esac
}

# Succeeds when version $1 is strictly greater than version $2.
version_gt() {
	[[ $1 != "$2" && $(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1) == "$1" ]]
}

classify() {
	local subject=$1 body=$2
	local breaking_re='^[a-z]+(\([^)]*\))?!:' change_re='^(feat|fix|perf)[(:]'
	if [[ $subject == "$bump_subject "* ]]; then
		echo bump
	elif [[ $subject =~ $breaking_re ]] || grep -qE '^BREAKING[ -]CHANGE:' <<<"$body"; then
		echo breaking
	elif [[ $subject =~ $change_re ]]; then
		echo "${BASH_REMATCH[1]}"
	else
		echo internal
	fi
}

# Sets tags, latest, base and base_sha for commit $1: base is the highest origin
# tag reachable from it, latest the highest origin tag overall.
load_tags() {
	local version sha
	tags=$(origin_tags)
	latest='' base='' base_sha=''
	while read -r version sha; do
		[[ -n $version ]] || continue
		latest=$version
		if git merge-base --is-ancestor "$sha" "$1" 2>/dev/null; then
			base=$version base_sha=$sha
		fi
	done <<<"$tags"
	[[ -n $base ]] || fail "no vX.Y.Z tag on origin is an ancestor of $1"
}

cmd_status() {
	git fetch -q origin main
	local pr
	pr=$(gh pr list --state open --search "\"$bump_subject\" in:title" --json number,title \
		--jq ".[] | select(.title | startswith(\"$bump_subject \")) | \"\(.number) \(.title)\"")
	if [[ -n $pr ]]; then
		pr=${pr%%$'\n'*}
		printf 'phase=pr\npr=%s\nversion=%s\n' "${pr%% *}" "${pr##* }"
		return
	fi

	# Only the newest bump or bump revert since the last release counts, older ones were superseded.
	# Subjects only, a body may quote the bump subject.
	local tags latest base base_sha log sha subject
	local merged_re="^chore\(release\): bump version to ($semver) \(#([0-9]+)\)$"
	load_tags "$(git rev-parse origin/main)"
	log=$(git log --format='%H %s' "$base_sha..origin/main")
	while read -r sha subject; do
		[[ $subject == "$bump_subject "* || $subject == "Revert \"$bump_subject "* ]] || continue
		if [[ $subject =~ $merged_re ]] && ! grep -q "^${BASH_REMATCH[1]} " <<<"$tags"; then
			printf 'phase=tag\nversion=%s\ncommit=%s\npr=%s\n' "${BASH_REMATCH[1]}" "$sha" "${BASH_REMATCH[5]}"
			return
		fi
		break
	done <<<"$log"
	echo phase=prepare
}

cmd_next() {
	local target=origin/main agreed='' request=''
	while (($#)); do
		case $1 in
		--to)
			target=${2:?--to needs a revision}
			shift 2
			;;
		--expect)
			agreed=${2:?--expect needs a version}
			[[ $agreed =~ ^$semver$ ]] || usage
			shift 2
			;;
		major | minor | patch)
			request=$1
			shift
			;;
		*)
			[[ $1 =~ ^$semver$ ]] || usage
			request=$1
			shift
			;;
		esac
	done

	git fetch -q origin main
	local target_sha tags latest base base_sha
	target_sha=$(git rev-parse --verify -q "$target^{commit}") || fail "unknown revision $target"
	load_tags "$target_sha"

	local -A count=([breaking]=0 [feat]=0 [fix]=0 [perf]=0 [internal]=0 [bump]=0)
	local lines=() sha subject body class
	while IFS=$'\x1f' read -r -d $'\x1e' sha subject body; do
		sha=${sha#$'\n'}
		class=$(classify "$subject" "$body")
		((++count[$class]))
		lines+=("$class"$'\t'"$sha"$'\t'"$subject")
	done < <(git log --format='%H%x1f%s%x1f%b%x1e' "$base_sha..$target_sha")

	local major=${latest%%.*} computed
	if ((count[breaking])); then
		if ((major)); then computed='major'; else computed='minor'; fi
	elif ((major && count[feat])); then
		computed='minor'
	elif ((count[feat] + count[fix] + count[perf])); then
		computed='patch'
	elif ((count[internal])); then
		computed='internal'
	else
		computed='none'
	fi

	local level=$computed auto='' next='' expect
	case $computed in
	major | minor | patch) auto=$(bump "$latest" "$computed") ;;
	esac
	case $request in
	'') next=$auto ;;
	major | minor | patch) level=$request next=$(bump "$latest" "$request") ;;
	*) level=explicit next=$request ;;
	esac
	# Later re-checks compare against the higher of the computed and the chosen version,
	# so only commits merged after this run can trip them.
	if [[ -n $auto ]] && version_gt "$auto" "$next"; then expect=$auto; else expect=$next; fi

	printf 'base=v%s\nbase_sha=%s\ntarget_sha=%s\nlatest=v%s\ncomputed=%s\nlevel=%s\nauto=%s\nversion=%s\nexpect=%s\n' \
		"$base" "$base_sha" "$target_sha" "$latest" "$computed" "$level" "$auto" "$next" "$expect"
	((${#lines[@]} == 0)) || printf '%s\n' "${lines[@]}"

	[[ $computed != none ]] || { echo "bump.sh: no commits since v$base" >&2; exit 3; }
	[[ -n $next ]] || { echo "bump.sh: only internal commits since v$base, pass a level to release anyway" >&2; exit 4; }
	version_gt "$next" "$latest" || fail "$next is not above the latest release v$latest"
	if [[ ${next%.*} == "${latest%.*}" && $latest != "$base" ]]; then
		fail "v$latest is not on the history of $target, release $next from release/${latest%.*} by hand"
	fi
	if [[ -n $agreed && -n $auto ]] && version_gt "$auto" "$agreed"; then
		echo "bump.sh: the commits now call for $auto, above the agreed $agreed" >&2
		exit 5
	fi
}

cmd_apply() {
	local version=${1:-}
	[[ $version =~ ^$semver$ ]] || usage
	cd "$(git rev-parse --show-toplevel)"

	sed -i -E "1,/^\)/ s/^(  version: )'[^']*'/\1'$version'/" meson.build
	git ls-files -z '*Cargo.toml' |
		xargs -0 sed -i -E "/^\[package\]/,/^\[/ s/^version = \"[^\"]*\"/version = \"$version\"/"
	cargo update --workspace --offline --quiet
	sed -i -E "s/^(    \"version\": )\"[^\"]*\"/\1\"$version\"/" web/package.json
	sed -i -E "/^        \"web\": \{$/,/^        \}/ s/^(            \"version\": )\"[^\"]*\"/\1\"$version\"/" package-lock.json

	if [[ $(head -1 debian/changelog) != "yanet2 ($version) "* ]]; then
		local name email
		name=$(git config user.name) email=$(git config user.email)
		[[ -n $name && -n $email ]] || fail "debian/changelog needs git user.name and user.email"
		{
			printf 'yanet2 (%s) unstable; urgency=medium\n\n  * Release %s.\n\n -- %s <%s>  %s\n\n' \
				"$version" "$version" "$name" "$email" "$(LC_ALL=C date -R)"
			cat debian/changelog
		} >debian/changelog.new
		mv debian/changelog.new debian/changelog
	fi

	cmd_check "$version"
}

cmd_check() {
	local version=${1:-} bad=0 file
	[[ $version =~ ^$semver$ ]] || usage
	cd "$(git rev-parse --show-toplevel)"

	expect() {
		[[ $2 == "$version" ]] && return
		echo "bump.sh: $1 has version '$2', want $version" >&2
		bad=1
	}
	expect meson.build "$(sed -n -E "1,/^\)/ s/^  version: '([^']*)',/\1/p" meson.build)"
	while IFS= read -r -d '' file; do
		grep -q '^\[package\]' "$file" || continue
		expect "$file" "$(sed -n -E '/^\[package\]/,/^\[/ s/^version = "([^"]*)"/\1/p' "$file")"
	done < <(git ls-files -z '*Cargo.toml')
	# Workspace members are the only Cargo.lock packages without a source.
	while read -r file got; do
		expect "Cargo.lock ($file)" "$got"
	done < <(awk '
		function flush() { if (name != "" && src == "") print name, ver }
		/^\[\[package\]\]$/ { flush(); name = ""; ver = ""; src = "" }
		/^name = / { name = $3 }
		/^version = / { ver = $3 }
		/^source = / { src = $3 }
		END { flush() }' Cargo.lock | tr -d '"')
	expect web/package.json "$(jq -r .version web/package.json)"
	expect package-lock.json "$(jq -r '.packages.web.version' package-lock.json)"
	expect debian/changelog "$(sed -n -E '1s/^yanet2 \(([^)]*)\).*/\1/p' debian/changelog)"

	((bad == 0)) || exit 1
	echo "all component versions are $version"
}

(($#)) || usage
command=$1
shift
case $command in
status) cmd_status "$@" ;;
next) cmd_next "$@" ;;
apply) cmd_apply "$@" ;;
check) cmd_check "$@" ;;
*) usage ;;
esac
