#!/usr/bin/env python3

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

from go_cgo_flags import PROBE_PREFIX


def symbols(paths):
    """Return the defined and the undefined global symbols of objects or archives."""
    result = subprocess.run(
        ["nm", "-A", "-g", "--format=posix", *paths], text=True,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    if result.returncode:
        raise RuntimeError(f"nm exited with {result.returncode}: {result.stderr.strip()}")
    defined, undefined = set(), set()
    for line in result.stdout.splitlines():
        # <file>[<member>]: <name> <type> [<value> <size>]
        _, separator, rest = line.partition(": ")
        fields = rest.split()
        if not separator or len(fields) < 2:
            continue
        if fields[1] == "U":
            undefined.add(fields[0])
        elif fields[1] not in ("w", "v"):
            defined.add(fields[0])
    return defined, undefined


def link_archives(parameters, build_root, project_archives):
    """Split a link line into the project archives on it and the symbols it forces.

    Each archive comes with whether it is linked whole.
    """
    archives, forced, whole = [], set(), False
    for word in parameters:
        if word == "-Wl,--whole-archive":
            whole = True
        elif word == "-Wl,--no-whole-archive":
            whole = False
        elif word.startswith(("-Wl,--require-defined=", "-Wl,--undefined=")):
            forced.add(word.split("=", 1)[1])
        elif word.endswith(".a") and not word.startswith("-"):
            path = (build_root / word).resolve()
            if path in project_archives:
                archives.append((path, whole))
    return archives, forced


def main():
    parser = argparse.ArgumentParser(
        description="Report project archives that an executable links but uses nothing from."
    )
    parser.add_argument("--build-root", required=True, type=Path)
    args = parser.parse_args()
    build_root = args.build_root.resolve()
    targets = json.loads((build_root / "meson-info/intro-targets.json").read_text())
    project_archives = {
        Path(target["filename"][0]).resolve()
        for target in targets
        if target["type"] == "static library" and not target["subproject"]
    }
    archive_symbols = {}

    def archive(path):
        if path not in archive_symbols:
            defined, undefined = symbols([path])
            archive_symbols[path] = defined, undefined - defined
        return archive_symbols[path]

    problems = []
    for target in targets:
        if target["type"] != "executable" or target["subproject"]:
            continue
        # A probe has no code of its own: the archives it links are its point.
        if target["name"].startswith(PROBE_PREFIX):
            continue
        executable = Path(target["filename"][0])
        # An executable that is not built yet has nothing to check.
        if not executable.exists():
            continue
        executable = executable.resolve()
        parameters = next(
            (source["parameters"] for source in target["target_sources"] if "linker" in source),
            [],
        )
        archives, forced = link_archives(parameters, build_root, project_archives)
        # An archive missing or newer than the executable means the executable
        # is stale: the build has not relinked it yet, so it has nothing to check.
        built = executable.stat().st_mtime
        if any(not path.exists() or path.stat().st_mtime > built for path, _ in archives):
            continue
        objects = sorted(
            Path(directory) / name
            for directory, _, names in os.walk(f"{executable}.p")
            for name in names if name.endswith(".o")
        )
        defined, needed = symbols(objects) if objects else (set(), set())
        needed = (needed | forced) - defined
        for path, whole in archives:
            # An archive that another one on the line references counts as used.
            #
            # That edge is a library's own dependency, even if this executable
            # pulls in none of the members that reference it.
            if whole or archive(path)[0] & needed or any(
                other != path and archive(other)[1] & archive(path)[0] for other, _ in archives
            ):
                continue
            problems.append(
                f"{executable.relative_to(build_root)}: "
                f"links {path.relative_to(build_root)} but uses nothing from it"
            )
    for problem in sorted(set(problems)):
        print(problem)
    if problems:
        print(
            "Drop these archives from the executables' dependencies, or from the dependency "
            "that hands them over when its library does not use them either.",
            file=sys.stderr,
        )
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
