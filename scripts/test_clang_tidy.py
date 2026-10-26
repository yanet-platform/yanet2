#!/usr/bin/env python3

import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest

import clang_tidy


SCRIPT = Path(clang_tidy.__file__).resolve()
PROJECT = SCRIPT.parent.parent


class ClangTidyReportTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        for executable in ("meson", "cc", "gcc", "clang-tidy-19", "git"):
            if not shutil.which(executable):
                raise RuntimeError(f"runner controls require {executable}")

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="tidy-controls-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name).resolve()
        self.root = self.directory / "source with spaces"
        self.build = self.root / "build"
        self.root.mkdir()
        self.invocations = 0
        shutil.copyfile(PROJECT / ".clang-tidy", self.root / ".clang-tidy")
        self.write("meson_options.txt", "option('extra_modules', type: 'array', value: [])\n")
        self.write("meson.build", """project('tidy-controls', 'c', default_options: ['b_ndebug=false'])
configuration = configuration_data()
configuration.set('CONFIGURED_VALUE', 1)
configure_file(output: 'configured.h', configuration: configuration)
foreach variant : [1, 2]
  executable('context-' + variant.to_string(), 'main.c',
    c_args: ['-DCONTEXT=' + variant.to_string(), '-include', 'configured.h'])
endforeach
subproject('dependency')
subdir('lib/rust')
executable('cgo-link-fixture', 'scripts/cgo_link_probe.c')
foreach extra_module : get_option('extra_modules')
  subdir('modules/' + extra_module)
endforeach
""")
        self.write("main.c", """#include "owned.h"
#include "subprojects/dependency/vendor.h"
#include <assert.h>
#ifdef NDEBUG
#error assertions must remain active
#endif
int main(int argc, char **argv) {
    (void)argv;
    assert(CONFIGURED_VALUE == 1);
    return argc > CONTEXT ? owned_bug() : vendor_bug();
}
""")
        self.write("owned.h", "static inline int owned_bug(void) { int *p = 0; return *p; }\n")
        self.write("unreached.h", "#define UNREACHED 1\n")
        self.write("subprojects/dependency/meson.build", "project('dependency', 'c')\nstatic_library('vendor', 'vendor.c')\n")
        self.write("subprojects/dependency/vendor.c", "int vendor(void) { return 0; }\n")
        self.write("subprojects/dependency/vendor.h", "static inline int vendor_bug(void) { int *p = 0; return *p; }\n")
        self.write("lib/rust/meson.build", "static_library('rust-shim', 'shim.c')\n")
        self.write("lib/rust/shim.c", "int shim(void) { return 0; }\n")
        self.write("scripts/cgo_link_probe.c", "int main(void) { return 0; }\n")

    def write(self, name, value):
        """Create a fixture input with any required source directories."""
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(value)
        return path

    def configure(self, *options, compiler=None):
        """Produce real Meson metadata without compiling the deliberately defective controls."""
        env = None
        if compiler:
            env = dict(os.environ, CC=compiler)
        result = subprocess.run(["meson", "setup", str(self.build), *options], cwd=self.root,
                                text=True, capture_output=True, env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def command(self, *options):
        """Allocate a fresh report destination for a real runner invocation."""
        self.invocations += 1
        output = self.directory / f"report-{self.invocations}"
        return [sys.executable, "-B", str(SCRIPT), "--root", str(self.root),
                "--build-dir", str(self.build), "--jobs", "2", "--output", str(output),
                *options], output

    def invoke(self, *options):
        """Return the entrypoint verdict and retained report from a configured fixture."""
        command, output = self.command(*options)
        result = subprocess.run(command, cwd=self.root, text=True, capture_output=True, timeout=30)
        self.assertTrue((output / "report.md").is_file(), result.stdout + result.stderr)
        return result, json.loads((output / "report.json").read_text()), output

    def slow_tidy(self):
        """Delegate configuration to the real tool and delay only translation-unit analysis."""
        path = self.directory / "slow-tidy"
        path.write_text(
            f"#!{sys.executable}\nimport os, sys, time\n"
            "if len(sys.argv) > 1 and sys.argv[1].endswith('.c'):\n    time.sleep(10)\n"
            f"os.execv({shutil.which('clang-tidy-19')!r}, ['clang-tidy-19'] + sys.argv[1:])\n"
        )
        path.chmod(0o755)
        return path

    def test_complete_report_groups_shared_header_and_preserves_every_context(self):
        for command in (["git", "init", "-q", str(self.root)],
                        ["git", "-C", str(self.root), "add", "--", "owned.h", "unreached.h"]):
            result = subprocess.run(command, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
        with (self.root / "main.c").open("a") as source:
            source.write("#if CONTEXT == 1\nint context_one_bug(void) { return sizeof(sizeof(int)); }\n#endif\n")
        self.configure()
        result, report, output = self.invoke()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr + str(report["errors"]))
        self.assertEqual(report["status"], "complete")
        self.assertEqual(report["counts"]["eligible_contexts"], 2)
        self.assertEqual(report["counts"]["completed_contexts"], 2)
        self.assertEqual(report["counts"]["eligible_sources"], 1)
        self.assertEqual(report["counts"]["exclusions_by_reason"], {
            "third-party": 1, "rust (including C shim)": 1,
            "cgo link probe (no cgo analysis)": 1,
        })
        shared = [finding for finding in report["findings"] if finding["file"] == "owned.h"]
        self.assertEqual(len(shared), 1, report["findings"])
        self.assertEqual(shared[0]["checker"], "clang-analyzer-core.NullDereference")
        self.assertEqual(shared[0]["disposition"], "unreviewed")
        self.assertEqual({item["target"] for item in shared[0]["occurrences"]}, {"context-1", "context-2"})
        self.assertFalse(any("vendor.h" in finding["file"] for finding in report["findings"]))
        variant = [finding for finding in report["findings"] if finding["checker"] == "bugprone-sizeof-expression"]
        self.assertEqual(len(variant), 1, report["findings"])
        self.assertEqual(variant[0]["file"], "main.c")
        self.assertEqual([item["target"] for item in variant[0]["occurrences"]], ["context-1"])
        self.assertEqual(report["headers"]["authority"], "git tracked and configured extra-module headers")
        self.assertEqual(set(report["headers"]["declared"]), {"owned.h", "unreached.h"})
        self.assertIn("build/configured.h", report["headers"]["reached"])
        self.assertIn("unreached.h", report["headers"]["unreached_gaps"])
        for context in report["contexts"]:
            self.assertIn("owned.h", context["headers"])
            self.assertIn("build/configured.h", context["headers"])
            self.assertIn("owned.h", context["native_headers"])
            self.assertNotIn("-DNDEBUG", context["compilation"]["arguments"])
            replay = json.loads((output / "contexts" / context["id"] / "compile_commands.json").read_text())
            self.assertEqual(replay, [context["compilation"]])
            self.assertIn("note:", (output / context["analysis"]["log"]).read_text())
            self.assertTrue((output / context["analysis_depfile"]).is_file())
        self.assertTrue(report["preparation"])
        for operation in report["preparation"]:
            self.assertTrue(operation["command"])
            self.assertTrue(Path(operation["directory"]).is_dir())
            self.assertIsInstance(operation["returncode"], int)
            self.assertGreaterEqual(operation["seconds"], 0)
            self.assertTrue((output / operation["log"]).is_file())
        self.assertIn("A  owned.h", (output / "report.md").read_text())
        self.assertTrue(set(report["profile"]["checks"]).issubset(report["enabled_checks"]))
        self.assertEqual(set(report["enabled_checks"]) - set(report["profile"]["checks"]),
                         set(report["automatic_dependencies"]))

    def test_refresh_includes_newly_registered_c_source(self):
        self.configure()
        self.write("new.c", "int main(void) { return 0; }\n")
        with (self.root / "meson.build").open("a") as source:
            source.write("executable('newly-registered', 'new.c')\n")
        result, report, _ = self.invoke()
        self.assertEqual(result.returncode, 0, report["errors"])
        self.assertEqual(report["counts"]["eligible_contexts"], 3)
        self.assertTrue(any(context["source"] == "new.c" and context["status"] == "complete"
                            for context in report["contexts"]))

    def test_missing_variant_fails_with_unchanged_source_set(self):
        self.configure()
        info = self.build / "meson-info"
        targets = json.loads((info / "intro-targets.json").read_text())
        options = json.loads((info / "intro-buildoptions.json").read_text())
        commands = json.loads((self.build / "compile_commands.json").read_text())
        ownership = clang_tidy.Ownership(self.root, self.build, options)
        complete, _, errors = clang_tidy.reconcile(targets, commands, ownership)
        self.assertEqual(errors, [])
        omitted = next(context["database_index"] for context in complete if context["target"] == "context-2")
        changed = commands[:omitted] + commands[omitted + 1:]
        self.assertEqual({entry["file"] for entry in commands}, {entry["file"] for entry in changed})
        missing, _, errors = clang_tidy.reconcile(targets, changed, ownership)
        self.assertTrue(any("missing compilation context" in error for error in errors), errors)
        self.assertEqual(sum(context["status"] == "missing" for context in missing), 1)
        refresh = self.directory / "meson-with-missing-variant"
        refresh.write_text(
            f"#!{sys.executable}\nimport json, pathlib, subprocess, sys\n"
            f"result = subprocess.run([{shutil.which('meson')!r}] + sys.argv[1:])\n"
            "if result.returncode == 0:\n"
            "    path = pathlib.Path(sys.argv[-1]) / 'compile_commands.json'\n"
            "    entries = json.loads(path.read_text())\n"
            "    selected = [i for i, entry in enumerate(entries) if entry['file'].endswith('/main.c')]\n"
            "    del entries[selected[-1]]\n"
            "    path.write_text(json.dumps(entries))\n"
            "sys.exit(result.returncode)\n"
        )
        refresh.chmod(0o755)
        result, report, _ = self.invoke("--meson", str(refresh))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report["status"], "incomplete")
        self.assertTrue(any("missing compilation context" in error for error in report["errors"]))
        self.assertEqual(report["counts"]["eligible_contexts"], 2)

    def test_foreign_build_is_rejected_before_refresh_or_default_output_mutates_it(self):
        self.configure()
        foreign = self.directory / "another source"
        foreign.mkdir()
        metadata = self.build / "meson-info/meson-info.json"
        before = metadata.read_bytes(), metadata.stat().st_mtime_ns
        build_entries = set(self.build.iterdir())
        result = subprocess.run([sys.executable, "-B", str(SCRIPT), "--root", str(foreign),
                                 "--build-dir", str(self.build)], cwd=self.root,
                                text=True, capture_output=True, timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(before, (metadata.read_bytes(), metadata.stat().st_mtime_ns))
        self.assertEqual(build_entries, set(self.build.iterdir()))
        outputs = list((foreign / ".agent-state/clang-tidy-reports").iterdir())
        self.assertEqual(len(outputs), 1)
        report = json.loads((outputs[0] / "report.json").read_text())
        self.assertEqual(report["status"], "incomplete")
        self.assertFalse((outputs[0] / "preparation/meson-refresh.log").exists())

    def test_symlinked_extra_module_is_analyzed_and_unreached_headers_are_gaps(self):
        external = self.directory / "external module"
        external.mkdir()
        (external / "meson.build").write_text("static_library('extra', 'extra.c')\n")
        (external / "extra.c").write_text('#include "extra.h"\nint extra(void) { return extra_bug(); }\n')
        (external / "extra.h").write_text("static inline int extra_bug(void) { int *p = 0; return *p; }\n")
        (external / "gap.h").write_text("#define GAP 1\n")
        (self.root / "modules").mkdir()
        (self.root / "modules/private").symlink_to(external, target_is_directory=True)
        self.configure("-Dextra_modules=private")
        result, report, _ = self.invoke()
        self.assertEqual(result.returncode, 0, report["errors"])
        self.assertEqual(report["counts"]["eligible_contexts"], 3)
        self.assertTrue(any(finding["file"] == "modules/private/extra.h" for finding in report["findings"]))
        self.assertIn("modules/private/gap.h", report["headers"]["unreached_gaps"])
        self.assertIn("modules/private/extra.h", report["headers"]["reached"])

    def test_clean_control_completes_without_findings(self):
        self.write("owned.h", "static inline int owned_bug(void) { int value = 1; return value; }\n")
        self.configure()
        result, report, _ = self.invoke()
        self.assertEqual(result.returncode, 0, report["errors"])
        self.assertEqual(report["findings"], [])

    def test_first_party_system_headers_keep_sizeof_findings_and_third_party_filter(self):
        meson = self.root / "meson.build"
        meson.write_text(meson.read_text().replace(
            "configuration = configuration_data()",
            "add_project_arguments('-isystem', meson.current_source_dir(), language: 'c')\n"
            "configuration = configuration_data()",
        ))
        self.write("owned.h", "#pragma GCC system_header\nstatic inline int owned_bug(void) { return sizeof(sizeof(int)); }\n")
        self.write("subprojects/dependency/vendor.h", "static inline int vendor_bug(void) { return sizeof(sizeof(int)); }\n")
        self.configure()
        result, report, _ = self.invoke()
        self.assertEqual(result.returncode, 0, report["errors"])
        findings = [finding for finding in report["findings"] if finding["checker"] == "bugprone-sizeof-expression"]
        self.assertEqual([finding["file"] for finding in findings], ["owned.h"])
        self.assertEqual({item["target"] for item in findings[0]["occurrences"]}, {"context-1", "context-2"})
        self.assertTrue(all("-isystem" in context["compilation"]["arguments"] for context in report["contexts"]))
        self.assertTrue(all("owned.h" in context["headers"] for context in report["contexts"]))

    def test_clang_analysis_and_native_compiler_header_sets_are_distinct(self):
        main = self.root / "main.c"
        main.write_text('#ifdef __clang__\n#include "clang-only.h"\n#else\n#include "gcc-only.h"\n#endif\n'
                        + main.read_text())
        self.write("clang-only.h", "#define CLANG_ONLY 1\n")
        self.write("gcc-only.h", "#define GCC_ONLY 1\n")
        self.configure(compiler=shutil.which("gcc"))
        result, report, _ = self.invoke()
        self.assertEqual(result.returncode, 0, report["errors"])
        for context in report["contexts"]:
            self.assertIn("clang-only.h", context["headers"])
            self.assertNotIn("gcc-only.h", context["headers"])
            self.assertIn("gcc-only.h", context["native_headers"])
            self.assertNotIn("clang-only.h", context["native_headers"])
        self.assertIn("clang-only.h", report["headers"]["reached"])
        self.assertNotIn("gcc-only.h", report["headers"]["reached"])
        self.assertIn("gcc-only.h", report["headers"]["unreached_gaps"])
        self.assertNotIn("clang-only.h", report["headers"]["unreached_gaps"])

    def test_successful_noisy_dependency_scan_separates_stderr_from_rules(self):
        meson = self.root / "meson.build"
        meson.write_text(meson.read_text().replace(
            "configuration = configuration_data()",
            "add_project_arguments('-v', language: 'c')\nconfiguration = configuration_data()",
        ))
        self.configure()
        result, report, output = self.invoke()
        self.assertEqual(result.returncode, 0, report["errors"])
        for context in report["contexts"]:
            scan = context["dependency_scan"]
            self.assertEqual(scan["returncode"], 0)
            rules = (output / scan["log"]).read_text()
            stderr = (output / scan["stderr_log"]).read_text()
            self.assertIn("tidy-input:", rules)
            self.assertNotIn("warning:", rules)
            self.assertTrue(stderr.strip())
            self.assertIn("-v", context["compilation"]["arguments"])

    def test_explicit_relative_tool_paths_survive_build_working_directories(self):
        for name, executable in (("tidy", "clang-tidy-19"), ("meson", "meson")):
            path = self.write("tools/" + name, f"#!{sys.executable}\nimport os, sys\n"
                              f"os.execv({shutil.which(executable)!r}, [{executable!r}] + sys.argv[1:])\n")
            path.chmod(0o755)
        self.configure()
        result, report, _ = self.invoke("--clang-tidy", "./tools/tidy", "--meson", "./tools/meson")
        self.assertEqual(result.returncode, 0, report["errors"])
        self.assertTrue(all(context["analysis"]["command"][0] == str(self.root / "tools/tidy")
                            for context in report["contexts"]))
        refresh = next(operation for operation in report["preparation"]
                       if operation["log"] == "preparation/meson-refresh.log")
        self.assertEqual(refresh["command"][0], str(self.root / "tools/meson"))

    def test_excluded_unity_targets_do_not_block_reporting_but_eligible_unity_does(self):
        self.write("subprojects/dependency/other.c", "int other(void) { return 0; }\n")
        self.write("subprojects/dependency/meson.build", "project('dependency', 'c')\n"
                   "static_library('vendor', ['vendor.c', 'other.c'], override_options: ['unity=on'])\n")
        self.write("lib/rust/other.c", "int other_shim(void) { return 0; }\n")
        self.write("lib/rust/meson.build", "static_library('rust-shim', ['shim.c', 'other.c'], override_options: ['unity=on'])\n")
        self.write("scripts/probe-helper.c", "int probe_helper(void) { return 0; }\n")
        meson = self.root / "meson.build"
        meson.write_text(meson.read_text().replace(
            "executable('cgo-link-fixture', 'scripts/cgo_link_probe.c')",
            "executable('cgo-link-fixture', ['scripts/cgo_link_probe.c', 'scripts/probe-helper.c'], "
            "override_options: ['unity=on'])",
        ))
        self.configure()
        result, report, _ = self.invoke()
        self.assertEqual(result.returncode, 0, report["errors"])
        self.assertEqual(report["counts"]["eligible_contexts"], 2)
        self.assertEqual(report["counts"]["excluded_contexts"], 3)
        self.assertTrue(all(exclusion["unity_sources"] for exclusion in report["exclusions"]))
        self.write("one.c", "int one(void) { return 1; }\n")
        self.write("two.c", "int two(void) { return 2; }\n")
        with meson.open("a") as source:
            source.write("static_library('eligible-unity', ['one.c', 'two.c'], override_options: ['unity=on'])\n")
        result, report, _ = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report["status"], "incomplete")
        self.assertTrue(any("first-party unity" in error for error in report["errors"]))

    def test_registered_external_first_party_source_fails_unresolved_ownership(self):
        outside = self.directory / "external.c"
        outside.write_text("int outside(void) { return 0; }\n")
        (self.root / "external.c").symlink_to(outside)
        with (self.root / "meson.build").open("a") as source:
            source.write("static_library('external', 'external.c')\n")
        self.configure()
        result, report, _ = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report["status"], "incomplete")
        self.assertTrue(any("unresolved first-party ownership" in error for error in report["errors"]))
        self.assertFalse(any(exclusion["source"] == str(outside) for exclusion in report["exclusions"]))

    def test_extra_module_alias_cannot_reclassify_rust_sources(self):
        meson = self.root / "meson.build"
        meson.write_text(meson.read_text().replace("subdir('lib/rust')\n", ""))
        (self.root / "modules").mkdir()
        (self.root / "modules/rust-alias").symlink_to(self.root / "lib/rust", target_is_directory=True)
        self.configure("-Dextra_modules=rust-alias")
        result, report, _ = self.invoke()
        self.assertEqual(result.returncode, 0, report["errors"])
        self.assertEqual(report["counts"]["eligible_contexts"], 2)
        self.assertEqual(report["counts"]["exclusions_by_reason"]["rust (including C shim)"], 1)

    def test_invalid_third_party_header_still_fails_parsing_and_retains_log(self):
        self.write("subprojects/dependency/vendor.h", "static inline int vendor_bug(void) { invalid C syntax }\n")
        self.configure()
        result, report, output = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report["status"], "incomplete")
        self.assertEqual(len(report["contexts"]), 2)
        self.assertTrue(all(context["analysis"]["returncode"] != 0 for context in report["contexts"]))
        self.assertTrue(all("vendor.h" in (output / context["analysis"]["log"]).read_text()
                            for context in report["contexts"]))

    def test_missing_generated_include_fails_without_silent_skip(self):
        with (self.root / "main.c").open("a") as source:
            source.write('#include "not-generated.h"\n')
        self.configure()
        result, report, output = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report["status"], "incomplete")
        self.assertEqual(len(report["contexts"]), 2)
        self.assertEqual(report["counts"]["completed_contexts"], 0)
        self.assertTrue(all("not-generated.h" in (output / context["dependency_scan"]["stderr_log"]).read_text()
                            for context in report["contexts"]))

    def test_missing_tool_build_or_invalid_configuration_retains_incomplete_report(self):
        self.configure()
        invalid_profile = self.directory / "invalid-profile.json"
        invalid_profile.write_text(json.dumps({"report_only": True, "llvm_major": 19,
                                              "checks": ["nonexistent-check"]}))
        for options in (("--clang-tidy", "/missing-clang-tidy"),
                        ("--meson", "/missing-meson"),
                        ("--build-dir", str(self.directory / "missing-build")),
                        ("--profile", str(invalid_profile))):
            with self.subTest(options=options):
                result, report, _ = self.invoke(*options)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(report["status"], "incomplete")
                self.assertTrue(report["errors"])
        empty_version = self.write("tools/empty-version", f"#!{sys.executable}\n")
        empty_version.chmod(0o755)
        result, report, _ = self.invoke("--clang-tidy", str(empty_version))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report["status"], "incomplete")
        self.assertTrue(any("LLVM major" in error for error in report["errors"]))
        self.assertNotIn("IndexError", result.stderr)
        metadata = self.build / "meson-info/meson-info.json"
        original = metadata.read_bytes()
        metadata.write_text("[]\n")
        try:
            command, _ = self.command()
            index = command.index("--output")
            del command[index:index + 2]
            result = subprocess.run(command, cwd=self.root, text=True, capture_output=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            reports = list((self.root / ".agent-state/clang-tidy-reports").glob("*/report.json"))
            self.assertEqual(len(reports), 1)
            malformed = json.loads(reports[0].read_text())
            self.assertEqual(malformed["status"], "incomplete")
            self.assertTrue(any("JSON object" in error for error in malformed["errors"]))
            self.assertNotIn("AttributeError", result.stderr)
        finally:
            metadata.write_bytes(original)
        with (self.root / ".clang-tidy").open("a") as config:
            config.write("  readability-braces-around-statements: true\n")
        result, report, output = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report["status"], "incomplete")
        self.assertTrue((output / "preparation/verify-config.log").is_file())

    def test_file_timeout_retains_incomplete_contexts_and_command_evidence(self):
        self.configure()
        result, report, output = self.invoke("--clang-tidy", str(self.slow_tidy()), "--timeout", "1.5")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(report["status"], "incomplete")
        self.assertEqual(len(report["contexts"]), 2, report["errors"])
        self.assertTrue(all(context["analysis"]["error"] == "timeout" for context in report["contexts"]))
        self.assertTrue(all((output / context["analysis"]["log"]).is_file() for context in report["contexts"]))

    def test_interruption_retains_an_incomplete_report_and_stops_workers(self):
        self.configure()
        command, output = self.command("--clang-tidy", str(self.slow_tidy()))
        process = subprocess.Popen(command, cwd=self.root, text=True,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline and not list(output.glob("contexts/*/analysis.log")):
                self.assertIsNone(process.poll(), "runner exited before the analysis phase")
                time.sleep(0.02)
            self.assertTrue(list(output.glob("contexts/*/analysis.log")))
            process.send_signal(signal.SIGTERM)
            process.communicate(timeout=10)
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate()
        self.assertNotEqual(process.returncode, 0)
        report = json.loads((output / "report.json").read_text())
        self.assertEqual(report["status"], "incomplete")
        self.assertIn("run interrupted", report["errors"])
        self.assertEqual(len(report["contexts"]), 2)
        self.assertTrue(all(context["status"] == "incomplete" for context in report["contexts"]))


if __name__ == "__main__":
    unittest.main()
