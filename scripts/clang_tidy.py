#!/usr/bin/env python3

"""Report the configured native C compilation contexts without admitting checks."""

import argparse
from collections import Counter, defaultdict, deque
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import sys
import tempfile
import threading
import time

from go_cdeps import dependency_paths


ANSI = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")
DIAGNOSTIC = re.compile(
    r"^(.+):(\d+):(\d+): (warning|error|fatal error|note): (.*)$"
)
CHECK = re.compile(r"^(.*) \[([^\]]+)\]$")
INCLUDE_FLAGS = ("-isystem", "-iquote", "-idirafter", "-I")
OUTPUT_FLAGS = ("-o", "-MF", "-MQ", "-MT", "-MJ")
DEPENDENCY_FLAGS = ("-c", "-MD", "-MMD", "-MP", "-MG", "-M", "-MM")


def write_json(path, value):
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(path)


def absolute(directory, value):
    return (directory / value).resolve()


def header_path_filter(paths, directory):
    patterns = [re.escape(str(path)) + "/" for path in paths]
    for path in paths:
        relative = os.path.relpath(path, directory)
        patterns.append(r"(\./)?[^/]" if relative == "." else re.escape(relative) + "/")
    return "^(" + "|".join(patterns) + ")"


def validate_build(root, build):
    metadata = json.loads((build / "meson-info/meson-info.json").read_text())
    if not isinstance(metadata, dict):
        raise ValueError("Meson metadata must be a JSON object")
    if (metadata.get("error") or Path(metadata["directories"]["source"]).resolve() != root
            or Path(metadata["directories"]["build"]).resolve() != build):
        raise ValueError("Meson metadata does not describe the requested source/build roots")
    return metadata


def compile_flags(arguments, compiler, source, directory):
    """Remove only compile outputs and the main input, preserving ordered flags."""
    if arguments[:len(compiler)] != compiler:
        raise ValueError(f"compiler differs from Meson metadata: {arguments[:len(compiler)]}")
    flags = []
    words = iter(arguments[len(compiler):])
    for word in words:
        if word.startswith("@"):
            raise ValueError("compiler response files are not supported")
        if word in OUTPUT_FLAGS:
            if next(words, None) is None:
                raise ValueError(f"missing compiler argument after {word}")
        elif word in DEPENDENCY_FLAGS:
            continue
        elif not word.startswith("-") and absolute(directory, word) == source:
            continue
        else:
            flags.append(word)
    return flags


def normalized_flags(flags, directory):
    """Resolve include directories only for context matching, never for replay."""
    normalized = []
    words = iter(flags)
    for word in words:
        for prefix in INCLUDE_FLAGS:
            if word == prefix:
                value = next(words, None)
                if value is None:
                    raise ValueError(f"missing include directory after {prefix}")
                normalized.append(prefix + str(absolute(directory, value)))
                break
            if word.startswith(prefix):
                normalized.append(prefix + str(absolute(directory, word[len(prefix):])))
                break
        else:
            normalized.append(word)
    return tuple(normalized)


class Ownership:
    def __init__(self, root, build, options):
        self.root = root
        self.build = build
        extras = next((item["value"] for item in options
                       if item["name"] == "extra_modules"), [])
        self.extras = [("modules/" + name, (root / "modules" / name).resolve(strict=True))
                       for name in extras]

    def label(self, path):
        path = path.resolve()
        for prefix, directory in [("build", self.build)] + self.extras + [("", self.root)]:
            if path.is_relative_to(directory):
                return "/".join(filter(None, (prefix, path.relative_to(directory).as_posix())))
        return str(path)

    def exclusion(self, path):
        path = path.resolve()
        if any(path.is_relative_to(directory.resolve()) for directory in
               (self.root / "subprojects", self.build / "subprojects")):
            return "third-party"
        if path.is_relative_to((self.root / "lib/rust").resolve()):
            return "rust (including C shim)"
        if path == (self.root / "scripts/cgo_link_probe.c").resolve():
            return "cgo link probe (no cgo analysis)"
        if path.is_relative_to((self.root / "scripts/uprobe").resolve()):
            return "standalone uprobe/BPF"
        label = self.label(path)
        if label.startswith(("subprojects/", "build/subprojects/")):
            return "third-party"
        if label.startswith("lib/rust/"):
            return "rust (including C shim)"
        if label == "scripts/cgo_link_probe.c":
            return "cgo link probe (no cgo analysis)"
        if label.startswith("scripts/uprobe/"):
            return "standalone uprobe/BPF"
        if path.is_relative_to(self.build) or path.is_relative_to(self.root):
            return None
        if any(path.is_relative_to(directory) for _, directory in self.extras):
            return None
        return "outside configured first-party roots"

    def header_filter(self, directory=None):
        roots = [self.root, self.build] + [directory for _, directory in self.extras]
        return header_path_filter(roots, directory or self.build)

    def excluded_header_filter(self, directory=None):
        paths = [self.root / "subprojects", self.build / "subprojects",
                 self.root / "lib/rust", self.root / "scripts/uprobe"]
        return header_path_filter(paths, directory or self.build)


def reconcile(targets, commands, ownership):
    """Require one database entry per registered source and ordered compiler context."""
    expected = defaultdict(deque)
    contexts = []
    exclusions = []
    errors = []
    for target in targets:
        for group in target["target_sources"]:
            if group.get("language") != "c":
                continue
            compiler = group["compiler"]
            flags = normalized_flags(group["parameters"], ownership.build)
            for value in group["sources"] + group.get("generated_sources", []):
                source = absolute(ownership.build, value)
                if source.suffix != ".c":
                    continue
                reason = ownership.exclusion(source)
                defined_in = absolute(ownership.root, target["defined_in"])
                target_reason = ownership.exclusion(defined_in)
                if target_reason and (reason is None or target_reason != "outside configured first-party roots"):
                    reason = target_reason
                if target.get("subproject"):
                    reason = "third-party"
                if target["name"].startswith("cgo-link-"):
                    reason = "cgo link probe (no cgo analysis)"
                if reason == "outside configured first-party roots":
                    errors.append(f"unresolved first-party ownership: {source} ({target['name']})")
                    reason = None
                if group.get("unity_sources") and not reason:
                    raise ValueError("eligible first-party unity C builds require a separate coverage decision")
                context = {
                    "id": f"{len(contexts) + 1:05d}" if not reason else None,
                    "source": ownership.label(source), "source_path": str(source),
                    "target": target["name"], "target_id": target["id"],
                    "compiler": compiler, "meson_parameters": group["parameters"],
                    "unity_sources": group.get("unity_sources", []),
                    "status": "missing", "exclusion": reason,
                }
                expected[(str(source), tuple(compiler), flags)].append(context)
                if reason:
                    exclusions.append(context)
                else:
                    contexts.append(context)
    for index, entry in enumerate(commands):
        directory = Path(entry["directory"]).resolve(strict=True)
        source = absolute(directory, entry["file"])
        if source.suffix != ".c":
            exclusions.append({"source": ownership.label(source),
                               "exclusion": "non-C compilation", "database_index": index})
            continue
        arguments = entry.get("arguments")
        if arguments is None:
            arguments = shlex.split(entry["command"])
        if not arguments or any(not isinstance(word, str) for word in arguments):
            raise ValueError("invalid compilation database arguments")
        candidates = [key for key in expected if key[0] == str(source)]
        match = None
        for key in candidates:
            compiler = list(key[1])
            if arguments[:len(compiler)] != compiler:
                continue
            flags = compile_flags(arguments, compiler, source, directory)
            if normalized_flags(flags, directory) == key[2] and expected[key]:
                match = expected[key].popleft()
                break
        if match is None:
            reason = ownership.exclusion(source)
            if reason and reason != "outside configured first-party roots":
                exclusions.append({"source": ownership.label(source), "exclusion": reason,
                                   "database_index": index})
            else:
                errors.append(f"unmatched compilation context #{index}: {ownership.label(source)}")
            continue
        match["database_index"] = index
        match["compilation"] = dict(entry, arguments=arguments)
        match["status"] = "excluded" if match["exclusion"] else "pending"
    for remaining in expected.values():
        for context in remaining:
            if not context["exclusion"]:
                errors.append(f"missing compilation context: {context['source']} ({context['target']})")
    if not contexts:
        errors.append("no eligible native C contexts")
    return contexts, exclusions, errors


class Runner:
    def __init__(self, output, timeout):
        self.output = output
        self.timeout = timeout
        self.stop = threading.Event()
        self.preparation = []

    def run(self, command, directory, log, separate_stderr=False):
        """Retain complete output and stop the entire subprocess group on failure to finish."""
        started = time.monotonic()
        result = {"command": command, "directory": str(directory), "log": log,
                  "returncode": None, "error": None}
        path = self.output / log
        path.parent.mkdir(parents=True, exist_ok=True)
        path.touch()
        output = ""
        stderr = ""
        if separate_stderr:
            result["stderr_log"] = str(Path(log).with_suffix(".stderr.log"))
        process = None
        try:
            if self.stop.is_set():
                raise InterruptedError("run interrupted")
            process = subprocess.Popen(command, cwd=directory, text=True, errors="replace",
                                       stdout=subprocess.PIPE,
                                       stderr=subprocess.PIPE if separate_stderr else subprocess.STDOUT,
                                       start_new_session=True)
            while True:
                try:
                    output, stderr = process.communicate(timeout=0.1)
                    break
                except subprocess.TimeoutExpired:
                    if self.stop.is_set() or time.monotonic() - started >= self.timeout:
                        result["error"] = "interrupted" if self.stop.is_set() else "timeout"
                        try:
                            os.killpg(process.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        output, stderr = process.communicate()
                        break
            result["returncode"] = process.returncode
        except (OSError, ValueError) as error:
            result["error"] = str(error)
        except BaseException:
            if process is not None and process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                output, stderr = process.communicate()
            raise
        finally:
            path.write_text(output)
            if separate_stderr:
                (self.output / result["stderr_log"]).write_text(stderr or "")
            result["seconds"] = round(time.monotonic() - started, 6)
            if log.startswith("preparation/"):
                self.preparation.append(result)
        return result, ANSI.sub("", output)

    def checked(self, command, directory, log):
        result, output = self.run(command, directory, log)
        if result["error"] or result["returncode"]:
            raise RuntimeError(f"{log}: {result['error'] or 'exit ' + str(result['returncode'])}")
        return result, output


def parse_diagnostics(output, directory, ownership):
    findings = []
    excluded = []
    for log_line, line in enumerate(output.splitlines(), 1):
        match = DIAGNOSTIC.match(line)
        if not match:
            if re.search(r"(?:^|: )(?:warning|error|fatal error): ", line):
                raise ValueError(f"unrecognized diagnostic: {line}")
            continue
        filename, row, column, severity, message = match.groups()
        if severity == "note":
            continue
        check = CHECK.match(message)
        if check is None:
            if severity == "warning":
                raise ValueError(f"diagnostic has no checker: {line}")
            checker = "clang-diagnostic-error"
        else:
            message, checker = check.groups()
        path = absolute(directory, filename)
        finding = {"file": ownership.label(path), "line": int(row),
                   "column": int(column), "severity": severity,
                   "checker": checker, "message": message, "disposition": "unreviewed",
                   "log_line": log_line}
        reason = ownership.exclusion(path)
        if reason:
            excluded.append(dict(finding, exclusion=reason))
        else:
            findings.append(finding)
    return findings, excluded


def dependency_headers(output, directory, source, ownership):
    paths = {absolute(directory, name) for name in dependency_paths(output)}
    if not paths or source not in paths:
        raise ValueError("compiler dependency rules omit the main input")
    for path in paths:
        if not path.is_file():
            raise ValueError(f"missing dependency: {path}")
    return sorted(ownership.label(path) for path in paths
                  if path != source and not ownership.exclusion(path))


def analyze(context, runner, ownership, tidy, config, checks, phase):
    context = dict(context)
    prefix = "contexts/" + context["id"]
    directory = Path(context["compilation"]["directory"])
    source = Path(context["source_path"])
    try:
        destination = runner.output / prefix
        if phase == "dependencies":
            destination.mkdir(parents=True)
            write_json(destination / "compile_commands.json", [context["compilation"]])
            flags = compile_flags(context["compilation"]["arguments"], context["compiler"],
                                  source, directory)
            command = context["compiler"] + flags + ["-M", "-MT", "tidy-input", "-MF", "-", str(source)]
            dependency, output = runner.run(command, directory, prefix + "/dependencies.log", separate_stderr=True)
            context["dependency_scan"] = dependency
            if dependency["error"] or dependency["returncode"]:
                raise RuntimeError("dependency scan failed")
            context["native_headers"] = dependency_headers(output, directory, source, ownership)
            context["status"] = "ready"
            return context
        depfile = destination / "analysis.d"
        context["analysis_depfile"] = prefix + "/analysis.d"
        # Front-end dependency output must survive the tool's driver-output stripping.
        dependency_args = ["-Xclang=-dependency-file", "-Xclang=" + str(depfile),
                           "-Xclang=-MT", "-Xclang=tidy-input", "-Xclang=-sys-header-deps"]
        command = [tidy, str(source), "-p", str(destination), "--config-file=" + str(config),
                   "--checks=" + checks, "--warnings-as-errors=-*", "--use-color=false",
                   "--system-headers",
                   "--header-filter=" + ownership.header_filter(directory),
                   "--exclude-header-filter=" + ownership.excluded_header_filter(directory)
                   ] + ["--extra-arg=" + value for value in dependency_args]
        analysis, output = runner.run(command, directory, prefix + "/analysis.log")
        context["analysis"] = analysis
        context["findings"], context["excluded_diagnostics"] = parse_diagnostics(
            output, directory, ownership,
        )
        if analysis["error"] or analysis["returncode"]:
            raise RuntimeError("clang-tidy analysis failed")
        if any(finding["severity"] != "warning" for finding in
               context["findings"] + context["excluded_diagnostics"]):
            raise RuntimeError("compiler errors in analysis")
        context["headers"] = dependency_headers(depfile.read_text(), directory, source, ownership)
        context["status"] = "complete"
    except (OSError, RuntimeError, ValueError) as error:
        context["status"] = "incomplete"
        context["error"] = str(error)
    return context


def execute_contexts(report, runner, ownership, args, config, checks, phase):
    contexts = report["contexts"]
    selected = [context for context in contexts
                if phase == "dependencies" or context["status"] == "ready"]
    pool = ThreadPoolExecutor(max_workers=args.jobs)
    futures = {}
    results = {}
    try:
        for context in selected:
            future = pool.submit(analyze, context, runner, ownership, args.clang_tidy,
                                 config, checks, phase)
            futures[future] = context
        for future in as_completed(futures):
            context = future.result()
            results[context["id"]] = context
    finally:
        if len(results) != len(selected):
            runner.stop.set()
        pool.shutdown(wait=True)
        for future in futures:
            context = future.result()
            results[context["id"]] = context
        report["contexts"] = [results.get(context["id"], context) for context in contexts]


def group_findings(contexts):
    groups = {}
    for context in contexts:
        for finding in context.get("findings", []):
            key = tuple(finding[field] for field in
                        ("checker", "file", "line", "column", "message"))
            if key not in groups:
                groups[key] = dict(finding, occurrences=[])
            groups[key]["occurrences"].append({
                "context": context["id"], "source": context["source"],
                "target": context["target"], "target_id": context["target_id"],
                "log": context["analysis"]["log"], "log_line": finding["log_line"],
            })
    return [groups[key] for key in sorted(groups)]


def header_inventory(ownership, contexts, runner):
    """Account for tracked and configured extra-module headers without synthetic contexts."""
    git, output = runner.run(["git", "-C", str(ownership.root), "ls-files", "-z", "--", "*.h", "*.inc"],
                             ownership.root, "preparation/header-inventory.log")
    if git["error"]:
        raise RuntimeError("header inventory failed: " + git["error"])
    if git["returncode"]:
        candidates = list(ownership.root.rglob("*.h")) + list(ownership.root.rglob("*.inc"))
        authority = "filesystem (unversioned source tree)"
    else:
        candidates = [ownership.root / value for value in output.split("\0") if value]
        authority = "git tracked and configured extra-module headers"
    for _, directory in ownership.extras:
        candidates.extend(directory.rglob("*.h"))
        candidates.extend(directory.rglob("*.inc"))
    declared = {ownership.label(path) for path in candidates
                if path.is_file() and not path.is_relative_to(ownership.build)
                and not ownership.exclusion(path.resolve())}
    reached = {header for context in contexts for header in context.get("headers", [])}
    native = {header for context in contexts for header in context.get("native_headers", [])}
    return {"authority": authority, "declared": sorted(declared), "reached": sorted(reached),
            "coverage_authority": "clang-tidy front-end dependency files",
            "native_reached": sorted(native),
            "unreached_gaps": sorted(declared - reached),
            "gap_policy": "No standalone header coverage or inferred compilation context."}


def render(report, output):
    contexts = report.get("contexts", [])
    report["findings"] = group_findings(contexts)
    report["counts"] = {
        "eligible_contexts": len(contexts),
        "eligible_sources": len({context["source"] for context in contexts}),
        "completed_contexts": sum(context["status"] == "complete" for context in contexts),
        "excluded_contexts": len(report.get("exclusions", [])),
        "excluded_sources": len({item["source"] for item in report.get("exclusions", [])}),
        "exclusions_by_reason": dict(Counter(item["exclusion"] for item in report.get("exclusions", []))),
        "grouped_findings": len(report["findings"]),
        "finding_instances": sum(len(item["occurrences"]) for item in report["findings"]),
    }
    lines = ["# Native clang-tidy report", "", f"Status: **{report['status']}**. Report only; all findings are **unreviewed**.",
             "No checker is admitted. Findings do not fail a complete run.", "",
             f"Revision: `{report.get('revision', 'unavailable')}`."]
    changes = report.get("worktree_changes")
    if changes:
        lines.extend(["Local worktree changes at invocation:", "", "```text", "\n".join(changes), "```", ""])
    else:
        lines.extend(["Local worktree changes: " + ("none." if changes == [] else "unavailable."), ""])
    lines.extend([
             "Scope: configured first-party Meson C contexts, including tests, benchmarks and reproducers.",
             "Rust, third-party targets and cgo link probes are excluded. Cgo analysis is not captured.",
             "", "## Coverage", "", "| Metric | Count |", "| --- | ---: |"])
    for label, key in (("Eligible C sources", "eligible_sources"), ("Eligible contexts", "eligible_contexts"),
                       ("Completed contexts", "completed_contexts"), ("Excluded sources", "excluded_sources"),
                       ("Excluded contexts", "excluded_contexts"), ("Grouped findings", "grouped_findings"),
                       ("Finding instances", "finding_instances")):
        lines.append(f"| {label} | {report['counts'][key]} |")
    lines.extend(["", "| Exclusion | Contexts |", "| --- | ---: |"])
    for reason, count in report["counts"]["exclusions_by_reason"].items():
        lines.append(f"| {reason} | {count} |")
    lines.extend(["", "Full commands, compiler arguments, exclusions and provenance: [report.json](report.json).", ""])
    if "tools" in report:
        versions = report["tools"].get("clang_tidy", "").splitlines()
        version = versions[0] if versions else "unavailable"
        lines.extend(["## Trial profile", "", f"Tool: `{version}`.", "",
                      "```text", "\n".join(report.get("profile", {}).get("checks", [])), "```", ""])
        if report.get("automatic_dependencies"):
            lines.extend(["Automatically enabled analyzer dependencies (recorded, not admitted):", "",
                          "```text", "\n".join(report["automatic_dependencies"]), "```", ""])
    if report.get("errors"):
        lines.extend(["## Incomplete run", ""] + ["- " + error for error in report["errors"]] + [""])
    for context in contexts:
        if context.get("error"):
            lines.append(f"- Context {context['id']} ({context['source']}, {context['target']}): {context['error']}.")
    if "headers" in report:
        headers = report["headers"]
        lines.extend(["", "## Header coverage", "", f"Clang analysis reached {len(headers['reached'])} first-party/generated includes; "
                      f"{len(headers['unreached_gaps'])} declared headers remain unreached.",
                      f"The separate configured-compiler inventory reached {len(headers['native_reached'])} includes; "
                      "it is input evidence, not analysis coverage.",
                      "These gaps have no synthetic context and no standalone coverage claim.", ""])
        lines.extend("- `" + path + "`" for path in headers["unreached_gaps"])
    lines.extend(["", "## Grouped findings (unreviewed)", ""])
    for finding in report["findings"]:
        message = finding["message"].replace("\n", " ")
        lines.extend([f"### {finding['file']}:{finding['line']}:{finding['column']}", "",
                      f"`{finding['checker']}`: {message}", ""])
        for occurrence in finding["occurrences"]:
            lines.append(f"- `{occurrence['source']}`; target `{occurrence['target']}`; "
                         f"context {occurrence['context']}: [full trace]({occurrence['log']}).")
        lines.append("")
    lines.extend(["## Timing", "", "| Phase | Seconds |", "| --- | ---: |"])
    for key, value in report.get("timing", {}).items():
        lines.append(f"| {key.removesuffix('_seconds').replace('_', ' ')} | {value:.3f} |")
    lines.append("")
    (output / "report.md").write_text("\n".join(lines))
    write_json(output / "report.json", report)


def run(args, output):
    started = time.monotonic()
    runner = Runner(output, args.timeout)
    report = {"status": "incomplete", "report_only": True, "errors": [],
              "started_utc": datetime.now(timezone.utc).isoformat(), "jobs": args.jobs,
              "timeout_seconds_per_command": args.timeout, "contexts": [], "timing": {},
              "preparation": runner.preparation}
    render(report, output)
    try:
        root = args.root.resolve(strict=True)
        build = args.build_dir.resolve(strict=True)
        report.update(source_root=str(root), build_root=str(build))
        validate_build(root, build)
        revision, value = runner.run(["git", "-C", str(root), "rev-parse", "--verify", "HEAD"],
                                      root, "preparation/revision.log")
        if revision["error"]:
            raise RuntimeError("revision discovery failed: " + revision["error"])
        report["revision"] = value.strip() if not revision["returncode"] else "unavailable (unversioned source tree)"
        dirty, value = runner.run(["git", "-C", str(root), "status", "--porcelain"],
                                   root, "preparation/worktree-status.log")
        if dirty["error"]:
            raise RuntimeError("worktree discovery failed: " + dirty["error"])
        report["worktree_changes"] = value.splitlines() if not dirty["returncode"] else None
        profile_path = args.profile.resolve(strict=True)
        profile = json.loads(profile_path.read_text())
        checks = profile["checks"]
        if (not profile.get("report_only") or not isinstance(checks, list) or not checks
                or len(set(checks)) != len(checks)
                or any(not re.fullmatch(r"[A-Za-z0-9_.-]+", check) or check.startswith("-") for check in checks)):
            raise ValueError("trial profile must be report-only with unique explicit check names")
        report["profile"] = profile
        write_json(output / "profile.json", profile)
        config = output / "clang-tidy.yaml"
        config.write_bytes((root / ".clang-tidy").read_bytes())
        report["config_sha256"] = hashlib.sha256(config.read_bytes()).hexdigest()
        check_arg = "-*," + ",".join(checks)
        _, version = runner.checked([args.clang_tidy, "--version"], root, "preparation/tidy-version.log")
        report["tools"] = {"clang_tidy": version.strip()}
        major = re.search(r"LLVM version (\d+)\.", version)
        if major is None or int(major[1]) != profile["llvm_major"]:
            raise ValueError(f"trial requires LLVM major {profile['llvm_major']}")
        common = [args.clang_tidy, "--config-file=" + str(config), "--checks=" + check_arg]
        runner.checked(common + ["--verify-config"], root, "preparation/verify-config.log")
        _, enabled = runner.checked(common + ["--list-checks"], root, "preparation/enabled-checks.log")
        actual_checks = [line.strip() for line in enabled.splitlines()[1:] if line.strip()]
        report["enabled_checks"] = actual_checks
        if not set(checks).issubset(actual_checks):
            raise ValueError("requested checks are missing from the enabled trial profile")
        report["automatic_dependencies"] = sorted(set(actual_checks) - set(checks))
        runner.checked(common + ["--dump-config"], root, "preparation/effective-config.yaml")
        runner.checked([args.meson, "setup", "--reconfigure", str(build)], root, "preparation/meson-refresh.log")
        info = build / "meson-info"
        metadata = validate_build(root, build)
        report["tools"]["meson"] = metadata["meson_version"]
        inputs = {"targets": info / "intro-targets.json", "buildoptions": info / "intro-buildoptions.json",
                  "compilers": info / "intro-compilers.json", "commands": build / "compile_commands.json"}
        snapshots = {name: json.loads(path.read_text()) for name, path in inputs.items()}
        (output / "metadata").mkdir()
        for name, value in snapshots.items():
            write_json(output / "metadata" / (name + ".json"), value)
        ownership = Ownership(root, build, snapshots["buildoptions"])
        report["configured_options"] = snapshots["buildoptions"]
        report["header_filter"] = ownership.header_filter()
        report["excluded_header_filter"] = ownership.excluded_header_filter()
        contexts, exclusions, errors = reconcile(snapshots["targets"], snapshots["commands"], ownership)
        report.update(contexts=contexts, exclusions=exclusions)
        report["errors"].extend(errors)
        render(report, output)
        if errors:
            raise RuntimeError("compilation contexts do not reconcile")
        compilers = sorted({tuple(context["compiler"]) for context in contexts})
        report["tools"]["compilers"] = []
        for index, compiler in enumerate(compilers):
            _, version = runner.checked(list(compiler) + ["--version"], build, f"preparation/compiler-{index}.log")
            report["tools"]["compilers"].append({"command": list(compiler), "version": version.strip()})
        dependency_started = time.monotonic()
        execute_contexts(report, runner, ownership, args, config, check_arg, "dependencies")
        report["timing"]["dependencies_seconds"] = round(time.monotonic() - dependency_started, 6)
        report["timing"]["preparation_seconds"] = round(time.monotonic() - started, 6)
        render(report, output)
        analysis_started = time.monotonic()
        try:
            execute_contexts(report, runner, ownership, args, config, check_arg, "analysis")
        finally:
            report["timing"]["analysis_seconds"] = round(time.monotonic() - analysis_started, 6)
        report["headers"] = header_inventory(ownership, report["contexts"], runner)
        if any(context["status"] != "complete" for context in report["contexts"]):
            raise RuntimeError("one or more native contexts did not complete")
        report["status"] = "complete"
    except KeyboardInterrupt:
        runner.stop.set()
        report["errors"].append("run interrupted")
        for context in report["contexts"]:
            if context["status"] != "complete":
                context["status"] = "incomplete"
                context.setdefault("error", "run interrupted")
    except (OSError, RuntimeError, ValueError, KeyError, TypeError) as error:
        report["errors"].append(str(error))
    finally:
        report["timing"]["total_seconds"] = round(time.monotonic() - started, 6)
        render(report, output)
    return report


def positive_int(value):
    number = int(value)
    if number < 1:
        raise argparse.ArgumentTypeError("must be positive")
    return number


def positive_float(value):
    number = float(value)
    if not 0 < number < float("inf"):
        raise argparse.ArgumentTypeError("must be finite and positive")
    return number


def interrupted(*_):
    raise KeyboardInterrupt()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--build-dir", type=Path, default=Path("build"))
    parser.add_argument("--profile", type=Path, default=Path(__file__).resolve().parent.parent / "lint/clang-tidy-report.json")
    parser.add_argument("--clang-tidy", default="clang-tidy-19")
    parser.add_argument("--meson", default="meson")
    parser.add_argument("--jobs", type=positive_int, default=min(8, os.cpu_count() or 1))
    parser.add_argument("--timeout", type=positive_float, default=120.0)
    parser.add_argument("--output", type=Path, help="fresh output directory (must not exist)")
    args = parser.parse_args()
    for name in ("clang_tidy", "meson"):
        executable = getattr(args, name)
        if os.sep in executable:
            setattr(args, name, str(Path(executable).resolve()))
    try:
        if args.output:
            output = args.output.resolve()
            output.mkdir(parents=True, exist_ok=False)
        else:
            try:
                validate_build(args.root.resolve(strict=True), args.build_dir.resolve(strict=True))
                parent = args.build_dir.resolve() / "clang-tidy-reports"
            except (OSError, ValueError, KeyError, TypeError):
                parent = args.root.resolve() / ".agent-state/clang-tidy-reports"
            parent.mkdir(parents=True, exist_ok=True)
            output = Path(tempfile.mkdtemp(prefix=datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ-"), dir=parent))
    except OSError as error:
        print(f"clang-tidy report: {error}", file=sys.stderr)
        return 1
    print(f"clang-tidy report: {output}", flush=True)
    signal.signal(signal.SIGTERM, interrupted)
    report = run(args, output)
    counts = report["counts"]
    print(f"clang-tidy report: {report['status']}; {counts['completed_contexts']}/{counts['eligible_contexts']} contexts; "
          f"{counts['grouped_findings']} unreviewed grouped findings; {output / 'report.md'}")
    return 0 if report["status"] == "complete" else 1


if __name__ == "__main__":
    sys.exit(main())
