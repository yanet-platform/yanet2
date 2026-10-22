#!/usr/bin/env python3
"""Reject uncached C/C++ compilation without modifying the configured build."""

import json
from pathlib import Path
import re
import shlex
import subprocess
import sys


build = sys.argv[1]
rules = subprocess.check_output(
    ["ninja", "-C", build, "-t", "rules"], text=True
).splitlines()
rules = [rule for rule in rules if re.fullmatch(r"(?:c|cpp)_COMPILER(?:_.*)?", rule)]
if not rules:
    sys.exit("ccache check FAILED: no configured C/C++ compiler rules")

# Ninja expands the actual compiler rule, including wrappers Meson introspection
# can omit from the compiler executable list.
commands = json.loads(subprocess.check_output(
    ["ninja", "-C", build, "-t", "compdb", *rules], text=True
))
if not commands:
    sys.exit("ccache check FAILED: no configured C/C++ compilation commands")
for command in commands:
    argv = shlex.split(command["command"])
    if not argv or Path(argv[0]).name != "ccache":
        sys.exit(
            f"ccache check FAILED: {build} has an uncached compiler command: "
            f"{command['command']}\n"
            "Manually reconfigure or recreate this build with ccache enabled "
            "(for explicit compilers use CC='ccache clang' and "
            "CXX='ccache clang++'). Existing build files have not been changed "
            "by this check. Merely installing ccache does not update an existing build."
        )
print(f"ccache check OK: {len(commands)} configured C/C++ compilation commands")
