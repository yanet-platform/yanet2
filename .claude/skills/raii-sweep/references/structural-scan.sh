#!/usr/bin/env bash
# Flag STRUCTURAL lifecycle smells the naming scanner (scan.sh) cannot see.
# Read-only: prints a report, writes nothing. Every flag is a candidate to
# READ, not a verdict — grep cannot prove a leak, it can only point at one.
#
# Output is split by PRECISION. The "likely-defect" heuristics aim to point at
# real bugs (read to confirm). The "decision/style inventory" heuristics point
# at the orthogonality/style surface, which is mostly conforming-or-decision-
# gated — use it to drive the new/free-orthogonality decision, NOT as a bug
# oracle. Genuine init error-path leaks / double-free / UAF still need the
# read-the-code leak-hunt workflow (SKILL.md Phase 2b).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

python3 - <<'EOF'
import glob
import re

SCOPE = "lib dataplane common api modules devices".split()

# Constructor verbs reach beyond new/create: spawn/copy/clone/dup/build own an
# allocation + error path too (counter_storage_spawn was invisible to the
# old suffix list and held a real garbage-read defect).
CTOR = "new|create|spawn|copy|clone|dup|build|make"
DTOR = "fini|free|destroy|release|cleanup|deinit"
# init allocates no struct (so it is not a CTOR), but it fills a caller-owned
# struct, owns an error path, and allocates field memory — it must be scanned.
LIFECYCLE = re.compile(rf"_({CTOR}|init|{DTOR})$")


def is_noise(path):
    return bool(
        re.search(r"(^|/)(tests?|fuzzing|unittest)/", path)
        or path.endswith("_test.c")
        or path.endswith("tests.c")
        or "/bench" in path
    )


files = []
for d in SCOPE:
    files += glob.glob(f"{d}/**/*.c", recursive=True)
prod = [f for f in files if "subprojects/" not in f and not is_noise(f)]

defn = re.compile(r"^([a-z_][a-z0-9_]*)\(")
# Return type on its own line, optionally storage-/inline-qualified: the repo's
# common `static void` / `static inline int` two-line definitions must match.
rettype = re.compile(
    r"^(static\s+)?(inline\s+)?(void|int|struct|[a-z_].*\*|uint\d+_t|size_t|bool)"
)

funcs = []
for f in prod:
    lines = open(f, errors="replace").read().split("\n")
    idx = 0
    while idx < len(lines):
        m = defn.match(lines[idx])
        if m and LIFECYCLE.search(m.group(1)) and idx > 0 and rettype.match(
            lines[idx - 1].strip()
        ):
            name, ret = m.group(1), lines[idx - 1].strip()
            cursor, depth, opened, proto, body = idx, 0, False, False, []
            while cursor < len(lines):
                line = lines[cursor]
                body.append(line)
                if "{" in line:
                    opened = True
                # A ';' before any '{' means this is a forward declaration,
                # not a definition (the widened static/inline return-type
                # match would otherwise swallow the next function as a body).
                if not opened and ";" in line:
                    proto = True
                    break
                depth += line.count("{") - line.count("}")
                if opened and depth <= 0:
                    break
                cursor += 1
            if proto:
                idx += 1
                continue
            funcs.append((name, ret, f, idx + 1, "\n".join(body)))
            idx = cursor + 1
        else:
            idx += 1


def self_param(name, body):
    # The lifecycle object is usually the first pointer parameter, but some
    # static helpers take a context first (e.g. module_ectx_free(cfg, ectx)),
    # so prefer the pointer parameter whose name matches the function's object.
    head = name[: LIFECYCLE.search(name).start()]
    tail = head.split("_")[-1]
    sig = body.split("(", 1)[1].split(")")[0] if "(" in body else ""
    ptr_idents = []
    for p in (q.strip() for q in sig.split(",")):
        if "*" not in p:
            continue
        toks = p.split("*")[-1].strip().split()
        if not toks:
            continue
        ident = toks[-1]
        if ident == head or ident == tail or head.endswith("_" + ident):
            return ident
        ptr_idents.append(ident)
    return ptr_idents[0] if ptr_idents else None


def has_cleanup_label(body):
    return bool(re.search(r"^\s*(err\w*|error\w*)\s*:", body, re.M))


no_zero, unchecked, leaf_idemp = [], [], []
multi_label, no_guard, fused, free_fini = [], [], [], []

for name, ret, f, ln, body in funcs:
    verb = LIFECYCLE.search(name).group(1)
    head = name[: LIFECYCLE.search(name).start()]
    is_ctor = bool(re.match(rf".*_({CTOR})$", name))
    is_init = name.endswith("_init")
    is_dtor = bool(re.match(rf".*_({DTOR})$", name))

    if is_ctor:
        allocs = re.search(r"=\s*(?:\([^)]*\)\s*)?(memory_balloc|malloc|calloc)\b", body)
        zeroes = re.search(r"\bmemset\s*\(", body) or re.search(r"=\s*\{\s*0?\s*\}", body)
        if allocs and has_cleanup_label(body) and not zeroes:
            no_zero.append((name, f"{f}:{ln}"))

        if verb in ("new", "create"):
            looks_fused = (
                re.search(rf"\b{re.escape(head)}_init\s*\(", body)
                or len(re.findall(r"memory_balloc|\b\w+_init\s*\(", body)) >= 2
                or re.search(r"\bfor\s*\(|\bwhile\s*\(", body)
            )
            if looks_fused:
                fused.append((name, f"{f}:{ln}"))

    # init allocates field memory too, so an unchecked allocation there is a
    # real OOM-deref candidate (the filter-compiler attr_init class).
    if is_ctor or is_init:
        for am in re.finditer(
            r"\b([A-Za-z_]\w*)\s*=\s*(?:\([^)]*\)\s*)?"
            r"(memory_balloc|malloc|calloc|rte_zmalloc|rte_malloc|rte_calloc)\b",
            body,
        ):
            v = am.group(1)
            if v in ("void", "struct", "const"):
                continue
            checked = (
                re.search(rf"\b{re.escape(v)}\s*(==|!=)\s*NULL", body)
                or re.search(rf"\(\s*!\s*{re.escape(v)}\s*\)", body)
                or re.search(rf"\(\s*!?\(?\s*{re.escape(v)}\s*=", body)
                or re.search(rf"\bif\s*\(\s*!?{re.escape(v)}\s*\)", body)
            )
            if not checked:
                unchecked.append((name, f"{f}:{ln}", v))

    if verb in ("init", "create") or name.endswith("_init"):
        labels = sorted(set(re.findall(r"^\s*(err\w*|error\w*)\s*:", body, re.M)))
        if len(labels) > 1:
            multi_label.append((name, f"{f}:{ln}", ",".join(labels)))

    if is_dtor:
        arg = self_param(name, body)
        inner = body.split("{", 1)[1] if "{" in body else ""
        # NULL self-safety: free(NULL)/memory_bfree(NULL,0) are already no-ops,
        # so a guardless free is a B4 defect ONLY when the body DEREFERENCES the
        # param (`arg->` / `arg[`). Token-precise guard match: a bare `<arg>)`
        # (e.g. container_of(<arg>, ...)) is NOT a guard — that bug cleared
        # route_module_config_free while flagging its identical sibling.
        if arg and name.endswith("_free"):
            derefs_self = re.search(rf"\b{re.escape(arg)}\s*(->|\[)", inner)
            guarded = (
                re.search(rf"{re.escape(arg)}\s*==\s*NULL", inner[:300])
                or re.search(rf"\(\s*!\s*{re.escape(arg)}\s*\)", inner[:300])
                or re.search(rf"\bif\s*\(\s*{re.escape(arg)}\s*\)\s*\{{?\s*$", inner[:300], re.M)
            )
            if derefs_self and not guarded:
                no_guard.append((name, f"{f}:{ln}", arg))

        # free that calls ANY child fini/free (name-agnostic) — the composite
        # teardown surface. `<head>_fini` alone missed `_data_fini` infixes and
        # cross-prefix `cp_module_fini`.
        if name.endswith("_free"):
            children = sorted(set(re.findall(r"\b(\w+_(?:fini|free|destroy))\s*\(", body)) - {name})
            if children:
                free_fini.append((name, f"{f}:{ln}", ",".join(children)[:60]))

        # Leaf idempotency: a fini-SEMANTICS destructor (fini/destroy — NOT
        # _free, which deallocs the struct itself) that bfrees a MEMBER without
        # a SET_OFFSET reset or a trailing memset may double-free on a second
        # call (the lpm_free class — note header-only static-inline leaves in
        # common/*.h escape this .c-only scan; the leak-hunt workflow covers
        # transitive idempotency there).
        if re.search(r"_(fini|destroy)$", name):
            bfrees_member = False
            for bm in re.finditer(r"memory_bfree\s*\(\s*[^,]+,\s*([^,]+),", body):
                target = bm.group(1).strip()
                if arg and re.fullmatch(rf"(\(\w[\w ]*\*\)\s*)?{re.escape(arg)}", target):
                    continue  # frees self -> pure dealloc, fine
                bfrees_member = True
            if bfrees_member and not re.search(r"SET_OFFSET_OF\s*\(", body) and not re.search(r"\bmemset\s*\(", body):
                leaf_idemp.append((name, f"{f}:{ln}"))


def section(title, rows):
    print(f"\n## {title}  ({len(rows)})")
    for r in rows:
        print("    " + "  ".join(str(c) for c in r))


print(f"lifecycle definitions scanned: {len(funcs)}")
print("\n=== LIKELY DEFECT (read to confirm) ===")
section("D-ZEROINIT  constructor allocs + has error path but never memsets (teardown may read garbage)", no_zero)
section("D-OOM  unchecked allocation inside a constructor body (multi-line aware)", unchecked)
section("D-IDEMP  fini bfrees a member with no reset/memset (may double-free on 2nd call)", leaf_idemp)
print("\n=== DECISION / STYLE INVENTORY (mostly conforming or decision-gated) ===")
section("S-STYLE  init/create with >1 cleanup label (stacked ladder -> single err: candidate)", multi_label)
section("S-NULLSAFE  free without a NULL self-guard (mostly FP: container_of frees are guarded by the Go binding)", no_guard)
section("S-ORTHO-NEW  fused new/create (convention: new = pure alloc; many are FAM-fill false positives)", fused)
section("S-ORTHO-FREE  free that calls a child fini/free (composite teardown; mostly conforming)", free_fini)
print("\nGenuine init error-path leak / double-free / UAF and fini idempotency on")
print("partial state need read-the-code judgement — run the leak-hunt workflow.")
EOF
