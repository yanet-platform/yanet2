#!/bin/sh
# Install on Ubuntu:
# sudo apt-get install -y moreutils git-buildpackage
# 1.2.1 - regular version
# 1.2.1.20220526234349 - snapshot
# 1.2.2 - next regular version
set -f
[ -z "$CI_DEBUG_TRACE" ] || set -x
# If not on a tag, generate a snapshot build
tag=$(git tag -l release/v* | sort -V | tail -1)
prevtag=$(git tag -l release/v* | sort -Vr | sed '2 !d')
version=${tag#release/v}
if [ "$(git log --exit-code $tag..HEAD | wc -l)" -ne 0 ]; then
	git_date=$(git log -1 --pretty=format:%at)
	case $(uname) in
	Darwin|FreeBSD)
		snapshot=$(date -r $git_date +%Y%m%d%H%M%S)
		;;
	*)
		snapshot=$(date --date="@$git_date" +%Y%m%d%H%M%S)
		;;
	esac
	parts=$(echo "$version" | awk -F'.' '{ print NF; }')
	[ "$parts" -lt 4 ] || version=${version%.*}
	version=$version.$snapshot
	prevtag=$tag
fi

echo "$version"
[ "anodch" != "a$1" ] || exit 0
gbp dch -R -D unstable -s $prevtag -N $version --full --spawn-editor=never --ignore-branch
