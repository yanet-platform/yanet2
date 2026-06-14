#!/usr/bin/env bash
# Inventory C lifecycle function definitions and flag asymmetric prefix
# families. Read-only: prints a report, writes nothing.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

SCOPE="lib dataplane common api modules devices"
SUFFIXES='new|init|fini|free|destroy|create|release|cleanup|deinit|teardown'

# Definitions: this codebase puts the return type on its own line, so a
# definition is a line that STARTS with the function name. Prototypes in
# headers match too, which is fine — we dedupe by name.
grep -rEn --include='*.h' --include='*.c' \
	"^([a-z_][a-z0-9_]*_(${SUFFIXES}))\(" ${SCOPE} 2>/dev/null \
	| sed -E 's/^([^:]+):([0-9]+):([a-z_][a-z0-9_]*)\(.*/\3\t\1:\2/' \
	| sort -u > /tmp/raii_defs.tsv

python3 - "$SUFFIXES" <<'EOF'
import collections
import re
import sys

suffix_re = re.compile(r"^(.*)_(%s)$" % sys.argv[1])
defs = collections.defaultdict(list)
for line in open("/tmp/raii_defs.tsv"):
    name, loc = line.rstrip("\n").split("\t")
    defs[name].append(loc)

groups = collections.defaultdict(dict)
for name, locs in defs.items():
    m = suffix_re.match(name)
    if m:
        groups[m.group(1)][m.group(2)] = locs

flagged = 0
print(f"{'PREFIX':40} {'SUFFIXES':28} FLAGS")
for prefix in sorted(groups):
    s = set(groups[prefix])
    flags = []
    if "init" in s and "fini" not in s:
        flags.append("init-without-fini")
    if "fini" in s and "init" not in s:
        flags.append("fini-without-init")
    if "new" in s and "free" not in s:
        flags.append("new-without-free")
    if "free" in s and not s & {"new", "init", "create"}:
        flags.append("free-without-allocator")
    bad = sorted(s & {"create", "destroy", "release", "cleanup", "deinit", "teardown"})
    if bad:
        flags.append("non-canonical:" + ",".join(bad))
    if flags:
        flagged += 1
        print(f"{prefix:40} {','.join(sorted(s)):28} {'; '.join(flags)}")
        for suf in sorted(s):
            for loc in groups[prefix][suf]:
                print(f"    {prefix}_{suf}  {loc}")

print(f"\ntotal prefixes: {len(groups)}, flagged: {flagged}")
EOF
