#!/usr/bin/env python3
"""Hooks that Claude Code and Codex run around an agent's tool calls.

Each mode reads one hook payload as JSON on stdin. A guard refuses the call
by exiting 2 with the reason on stderr, which both clients honour; every
other outcome is silent, and a payload that cannot be read lets the call
through. The guards catch accidents, not adversaries: a write through the
shell or a git call inside a script passes unseen. Only checkouts of the
repository that holds this file are guarded or formatted.

usage: agent_hook.py guard-writes | guard-git | format
"""

import json
import os
import re
import shlex
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# Claude edits name the file; a Codex patch names each file in a header line.
PATCH_HEADER = re.compile(r"^\*\*\* (Add|Update|Delete) File: (.+?)\s*$|^\*\*\* Move to: (.+?)\s*$", re.M)
HEREDOC = re.compile(r"(?<!<)<<(?!<)-?\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\1")
ASSIGNMENT = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=")
SHELL_PUNCTUATION = set("();<>|&")
WHOLE_TREE_PATHSPECS = {".", "./", ":/", ":/:", ":(top)", "*"}

# Options that take the next argument as their value, so that value is not
# read as a flag.
COMMIT_SHORT_VALUES = set("mFCct")
COMMIT_LONG_VALUES = {
    "--message", "--file", "--reuse-message", "--reedit-message", "--author", "--date",
    "--fixup", "--squash", "--template", "--trailer", "--cleanup",
}
GIT_GLOBAL_VALUES = {"-c", "--git-dir", "--work-tree", "--namespace", "--config-env"}


def git(cwd, *args):
    """Returns the stripped output of a git command, or None when it fails."""
    try:
        result = subprocess.run(
            ["git", "-C", cwd] + list(args),
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            universal_newlines=True,
            timeout=10,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    return result.stdout.strip()


_own_common_dir = []


def own_checkout(path):
    """Returns the root of the checkout holding a path when that checkout belongs to this repository."""
    directory = path if os.path.isdir(path) else os.path.dirname(path)
    while directory and not os.path.isdir(directory):
        directory = os.path.dirname(directory)
    if not directory:
        return None
    if not _own_common_dir:
        _own_common_dir.append(git(HERE, "rev-parse", "--path-format=absolute", "--git-common-dir"))
    common = git(directory, "rev-parse", "--path-format=absolute", "--git-common-dir")
    if common is None or common != _own_common_dir[0]:
        return None
    return git(directory, "rev-parse", "--show-toplevel")


def edited_paths(payload, deleted=True):
    """Returns the absolute paths an edit or patch tool call writes."""
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict):
        return []
    cwd = payload.get("cwd") or os.getcwd()
    paths = [tool_input[key] for key in ("file_path", "notebook_path") if isinstance(tool_input.get(key), str)]
    if payload.get("tool_name") == "apply_patch" and isinstance(tool_input.get("command"), str):
        for match in PATCH_HEADER.finditer(tool_input["command"]):
            if match.group(3):
                paths.append(match.group(3))
            elif deleted or match.group(1) != "Delete":
                paths.append(match.group(2))
    return [os.path.normpath(os.path.join(cwd, path)) for path in paths]


def guard_writes(payload):
    for path in edited_paths(payload):
        root = own_checkout(path)
        if root is None or git(root, "symbolic-ref", "--quiet", "--short", "HEAD") != "main":
            continue
        relative = os.path.relpath(path, root)
        if git(root, "check-ignore", "-q", "--", relative) is not None:
            continue
        return (
            f"{relative}: this checkout is on main, which takes writes only under gitignored paths; "
            "make the change in a task worktree (AGENTS.md, Worktrees)"
        )
    return None


def strip_heredocs(script):
    """Drops here-document bodies, which are data rather than commands."""
    lines = []
    terminators = []
    for line in script.split("\n"):
        if terminators:
            if line.strip() == terminators[0]:
                terminators.pop(0)
            continue
        lines.append(line)
        terminators.extend(match.group(2) for match in HEREDOC.finditer(line))
    return "\n".join(lines)


def separate_lines(script):
    """Turns each newline outside quotes into a command separator."""
    out = []
    quote = None
    escaped = False
    for char in script:
        if escaped:
            escaped = False
        elif char == "\\" and quote != "'":
            escaped = True
        elif quote:
            if char == quote:
                quote = None
        elif char in "'\"":
            quote = char
        elif char == "\n":
            char = ";"
        out.append(char)
    return "".join(out)


def git_calls(script, cwd):
    """Returns (arguments after git, directory) for each git command in a shell script.

    The directory follows cd between chained commands and git's own -C. A
    script that does not tokenize yields no calls.
    """
    lexer = shlex.shlex(separate_lines(strip_heredocs(script)), posix=True, punctuation_chars=True)
    lexer.whitespace_split = True
    try:
        tokens = list(lexer)
    except ValueError:
        return []
    calls = []
    command = []
    for token in tokens + [";"]:
        if token and set(token) <= SHELL_PUNCTUATION:
            cwd = record_call(command, cwd, calls)
            command = []
        else:
            command.append(token)
    return calls


def record_call(command, cwd, calls):
    while command and (ASSIGNMENT.match(command[0]) or command[0] in ("command", "exec", "time", "nohup")):
        command = command[1:]
    if not command:
        return cwd
    if command[0] in ("cd", "pushd") and len(command) > 1:
        return os.path.normpath(os.path.join(cwd, os.path.expanduser(command[1])))
    if os.path.basename(command[0]) != "git":
        return cwd
    args = command[1:]
    directory = cwd
    while args and args[0].startswith("-"):
        option = args.pop(0)
        if option == "-C" and args:
            directory = os.path.normpath(os.path.join(directory, os.path.expanduser(args.pop(0))))
        elif option in GIT_GLOBAL_VALUES and args:
            args.pop(0)
    if args:
        calls.append((args, directory))
    return cwd


def short_flags(arg):
    return arg[1:] if len(arg) > 1 and arg.startswith("-") and not arg.startswith("--") else ""


def commit_flags(args):
    """Returns the short flags a commit command line sets, leaving out option values."""
    found = set()
    skip_value = False
    for arg in args:
        if skip_value:
            skip_value = False
            continue
        if arg == "--":
            break
        if arg in COMMIT_LONG_VALUES:
            skip_value = True
        flags = short_flags(arg)
        for index, flag in enumerate(flags):
            found.add(flag)
            if flag in COMMIT_SHORT_VALUES:
                skip_value = index == len(flags) - 1
                break
    return found


def skips_hooks(subcommand, args):
    skip_value = False
    for arg in args:
        if skip_value:
            skip_value = False
            continue
        if arg == "--":
            break
        if arg == "--no-verify":
            return True
        if arg in COMMIT_LONG_VALUES:
            skip_value = True
        if subcommand != "commit":
            continue
        flags = short_flags(arg)
        for index, flag in enumerate(flags):
            if flag == "n":
                return True
            if flag in COMMIT_SHORT_VALUES:
                skip_value = index == len(flags) - 1
                break
    return False


def stages_whole_tree(args):
    pathspecs = []
    whole = update = False
    options_done = False
    for arg in args:
        if not options_done and arg == "--":
            options_done = True
        elif not options_done and arg.startswith("--"):
            whole = whole or arg in ("--all", "--no-ignore-removal")
            update = update or arg == "--update"
        elif not options_done and short_flags(arg):
            whole = whole or "A" in arg
            update = update or "u" in arg
        else:
            pathspecs.append(arg)
    return any(spec in WHOLE_TREE_PATHSPECS for spec in pathspecs) or ((whole or update) and not pathspecs)


def git_verdict(args, dirty):
    """Returns why a git command is refused, or None; the callback reports uncommitted changes."""
    subcommand, rest = args[0], args[1:]
    flags = "".join(short_flags(arg) for arg in rest)
    if (subcommand == "add" and stages_whole_tree(rest)) or (
        subcommand == "commit" and ("--all" in rest or "a" in commit_flags(rest))
    ):
        return f"git {subcommand} of the whole tree; stage the change by explicit paths (ship-pr)"
    if subcommand == "clean" and ("x" in flags or "X" in flags) and not ("--dry-run" in rest or "n" in flags):
        return "git clean -x also deletes ignored trees (build/, node_modules/, .arch/, private modules); remove paths explicitly"
    if subcommand in ("commit", "merge") and skips_hooks(subcommand, rest):
        return "--no-verify skips the commit hooks (commitlint, stylelint, main protection); fix what they report"
    if subcommand == "stash" and not (rest and rest[0] in ("list", "show")):
        return (
            "the stash stack is shared by every worktree and agent session; set work aside with a WIP commit "
            "and read old versions with git show <rev>:<path>"
        )
    discards = (
        (subcommand == "reset" and ("--hard" in rest or "--merge" in rest))
        or (subcommand == "checkout" and ("--" in rest or "." in rest or "-f" in rest or "--force" in rest))
        or (subcommand == "restore" and ("--worktree" in rest or "W" in flags or not ("--staged" in rest or "S" in flags)))
        or (subcommand == "clean" and ("--force" in rest or "f" in flags) and not ("--dry-run" in rest or "n" in flags))
        or (subcommand == "switch" and ("--discard-changes" in rest or "-f" in rest or "--force" in rest))
    )
    if discards and dirty():
        return f"git {subcommand} would discard uncommitted changes in this checkout; commit them or work in another worktree"
    return None


def guard_git(payload):
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict) or not isinstance(tool_input.get("command"), str):
        return None
    for args, directory in git_calls(tool_input["command"], payload.get("cwd") or os.getcwd()):
        root = own_checkout(directory)
        if root is None:
            continue
        reason = git_verdict(args, lambda: bool(git(root, "status", "--porcelain", "--ignore-submodules=dirty")))
        if reason:
            return reason
    return None


def read_first(path, pattern):
    try:
        with open(path) as source:
            match = re.search(pattern, source.read(), re.M)
    except OSError:
        return None
    return match.group(1) if match else None


def crate_edition(path, root):
    """Returns the edition declared by the nearest crate manifest, or 2024 when none declares one."""
    directory = os.path.dirname(path)
    while directory.startswith(root):
        edition = read_first(os.path.join(directory, "Cargo.toml"), r'^edition\s*=\s*"(\d{4})"')
        if edition:
            return edition
        if directory == root:
            break
        directory = os.path.dirname(directory)
    return "2024"


def formatter(path, root):
    """Returns the formatter command for a file, or None when the local tool differs from the one CI pins."""
    if path.endswith(".go") and not path.endswith(".pb.go"):
        return ["gofmt", "-w", path]
    if path.endswith((".c", ".h")):
        wanted = read_first(os.path.join(root, ".github/actions/clang-format/Dockerfile"), r"^ARG VERSION=(\d+)")
        try:
            version = subprocess.run(
                ["clang-format", "--version"], stdout=subprocess.PIPE, universal_newlines=True, timeout=10
            ).stdout
        except (OSError, subprocess.SubprocessError):
            return None
        found = re.search(r"version (\d+)\.", version)
        if wanted and found and found.group(1) == wanted:
            return ["clang-format", "-i", "--style=file", path]
        return None
    if path.endswith(".rs"):
        toolchain = read_first(os.path.join(root, ".github/workflows/rust-check.yml"), r"toolchain:\s*(nightly-[0-9-]+)")
        if toolchain:
            return ["rustfmt", "+" + toolchain, "--edition", crate_edition(path, root), path]
    return None


def format_files(payload):
    for path in edited_paths(payload, deleted=False):
        if not os.path.isfile(path):
            continue
        root = own_checkout(path)
        if root is None:
            continue
        top = os.path.relpath(path, root).split(os.sep)[0]
        if top in ("subprojects", "node_modules", "target") or top.startswith("build"):
            continue
        command = formatter(path, root)
        if command is None:
            continue
        try:
            subprocess.run(
                command,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=30,
                env=dict(os.environ, RUSTUP_AUTO_INSTALL="0"),
            )
        except (OSError, subprocess.SubprocessError):
            pass


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    if mode not in ("guard-writes", "guard-git", "format"):
        print(__doc__.strip().splitlines()[-1], file=sys.stderr)
        return 1
    try:
        payload = json.load(sys.stdin)
    except ValueError:
        return 0
    if not isinstance(payload, dict):
        return 0
    if mode == "format":
        format_files(payload)
        return 0
    reason = guard_writes(payload) if mode == "guard-writes" else guard_git(payload)
    if reason:
        print(f"blocked by .agents/hooks: {reason}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
