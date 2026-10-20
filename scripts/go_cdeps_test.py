#!/usr/bin/env python3
"""Verify native fingerprints against ordinary cached Go builds."""

import argparse
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
HELPER = ROOT / "scripts/go_cdeps.py"
TOOLS = None


def run_command(command, *, cwd, env=None, check=True):
    """Capture a real tool invocation and preserve its diagnostics on failure."""
    result = subprocess.run(
        [str(argument) for argument in command],
        cwd=cwd,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    if check and result.returncode:
        raise AssertionError(
            f"Command failed ({result.returncode}): {shlex.join(map(str, command))}"
            f"\n{result.stdout}"
        )
    return result


def file_state(path):
    """Include file identity so an unchanged rewrite cannot pass as a no-op."""
    stat = path.stat()
    return (path.read_bytes(), stat.st_ino, stat.st_mtime_ns, stat.st_mode)


def go_tool_commands(output, names=None):
    """Select executed compiler, CGO and linker commands from Go's real trace."""
    if names is None:
        names = {
            "cgo", "compile", "asm", "pack", "link", "cc", "gcc", "g++",
            "clang", "clang++", Path(TOOLS.cc).name,
        }
    commands = []
    for line in output.splitlines():
        try:
            words = shlex.split(line)
        except ValueError:
            continue
        while words and re.match(r"^[A-Za-z_][A-Za-z_0-9]*=", words[0]):
            words.pop(0)
        if words and Path(words[0]).name in names:
            commands.append(line)
    return commands


class FingerprintTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="yanet cdeps unit ")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.archive = self.root / "native archive.a"
        self.header = self.root / "native header.h"
        self.marker = self.root / "package/zz_yanet_cdeps.c"
        self.stamp = self.root / "build/native.stamp"
        self.marker.parent.mkdir()
        self.stamp.parent.mkdir()
        self.archive.write_bytes(b"archive-one")
        self.header.write_bytes(b"header-one")

    def prepare(self, *inputs, check=True, root=None, marker=None, stamp=None):
        """Run the production CLI with explicit files and an isolated root."""
        return run_command(
            [
                sys.executable,
                HELPER,
                "--root",
                root or self.root,
                "--output",
                marker or self.marker,
                "--stamp",
                stamp or self.stamp,
                *(inputs or (self.archive, self.header)),
            ],
            cwd=self.root,
            check=check,
        )

    def alias(self, target, spelling):
        """Give an existing file equivalent direct, parent-segment or symlink paths."""
        if spelling == "direct":
            return target
        if spelling == "parent segment":
            directory = target.parent / "alias directory"
            directory.mkdir(exist_ok=True)
            return directory / ".." / target.name
        link = target.with_name(target.name + ".link")
        if not link.is_symlink():
            link.symlink_to(target)
        return link

    def assert_files_preserved(self, previous):
        """Check both published outputs and every explicit native input."""
        for path, state in previous.items():
            self.assertEqual(file_state(path), state, str(path))

    def test_output_aliases_are_rejected_without_modifying_outputs_or_inputs(self):
        self.prepare()
        previous = {
            path: file_state(path)
            for path in (self.marker, self.stamp, self.archive, self.header)
        }
        for output in ("marker", "stamp"):
            target = self.stamp if output == "marker" else self.marker
            for spelling in ("direct", "parent segment", "symlink"):
                with self.subTest(output=output, spelling=spelling):
                    result = self.prepare(
                        **{output: self.alias(target, spelling)}, check=False
                    )
                    self.assertNotEqual(result.returncode, 0)
                    self.assert_files_preserved(previous)

    def test_output_aliasing_any_native_input_is_rejected_without_modification(self):
        self.prepare()
        previous = {
            path: file_state(path)
            for path in (self.marker, self.stamp, self.archive, self.header)
        }
        for output in ("marker", "stamp"):
            for target in (self.archive, self.header):
                for spelling in ("direct", "parent segment", "symlink"):
                    with self.subTest(
                        output=output, input=target.name, spelling=spelling
                    ):
                        result = self.prepare(
                            **{output: self.alias(target, spelling)}, check=False
                        )
                        self.assertNotEqual(result.returncode, 0)
                        self.assert_files_preserved(previous)

    def test_unchanged_inputs_preserve_marker_and_stamp_identity(self):
        self.prepare()
        marker = file_state(self.marker)
        stamp = file_state(self.stamp)
        self.prepare()
        self.assertEqual(file_state(self.marker), marker)
        self.assertEqual(file_state(self.stamp), stamp)

    def test_input_order_and_absolute_spelling_preserve_fingerprint(self):
        self.prepare(self.header.name, self.archive.name)
        marker = file_state(self.marker)
        stamp = file_state(self.stamp)
        self.prepare(self.archive, self.header)
        self.assertEqual(file_state(self.marker), marker)
        self.assertEqual(file_state(self.stamp), stamp)

    def test_changed_bytes_with_restored_mtime_change_fingerprint(self):
        self.prepare()
        previous = self.marker.read_bytes()
        stat = self.archive.stat()
        self.archive.write_bytes(b"archive-two")
        os.utime(self.archive, ns=(stat.st_atime_ns, stat.st_mtime_ns))
        self.prepare()
        self.assertNotEqual(self.marker.read_bytes(), previous)

    def test_missing_later_input_preserves_both_published_files(self):
        self.prepare()
        marker = file_state(self.marker)
        stamp = file_state(self.stamp)
        self.archive.write_bytes(b"changed-first-input")
        self.header.unlink()
        result = self.prepare(check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(file_state(self.marker), marker)
        self.assertEqual(file_state(self.stamp), stamp)

    def test_stamp_staging_failure_preserves_existing_marker(self):
        self.prepare()
        marker = file_state(self.marker)
        stamp = file_state(self.stamp)
        self.archive.write_bytes(b"archive-two")
        result = self.prepare(stamp=self.root / "absent/stamp", check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(file_state(self.marker), marker)
        self.assertEqual(file_state(self.stamp), stamp)
        self.assertEqual(list(self.marker.parent.glob(".*")), [])

    def test_identical_relative_files_have_identical_fingerprints_across_roots(self):
        self.prepare()
        other = self.root / "another project root"
        other.mkdir()
        for path in (self.archive, self.header):
            shutil.copyfile(path, other / path.name)
        marker = other / "marker.c"
        stamp = other / "stamp"
        self.prepare(
            other / self.archive.name,
            other / self.header.name,
            root=other,
            marker=marker,
            stamp=stamp,
        )
        self.assertEqual(marker.read_bytes(), self.marker.read_bytes())
        self.assertEqual(stamp.read_bytes(), self.stamp.read_bytes())

    def test_file_paths_are_part_of_fingerprint(self):
        self.prepare(self.archive)
        previous = self.marker.read_bytes()
        renamed = self.archive.with_name("renamed archive.a")
        self.archive.rename(renamed)
        self.prepare(renamed)
        self.assertNotEqual(self.marker.read_bytes(), previous)

    def test_path_and_content_boundaries_cannot_alias(self):
        first = self.root / "a"
        first.write_bytes(b"bc")
        second = self.root / "ab"
        second.write_bytes(b"c")
        self.prepare(first)
        previous = self.stamp.read_bytes()
        self.prepare(second)
        self.assertNotEqual(self.stamp.read_bytes(), previous)

    def test_changed_publication_preserves_prior_bytes_for_existing_readers(self):
        self.prepare()
        with self.marker.open("rb") as marker, self.stamp.open("rb") as stamp:
            previous_marker = self.marker.read_bytes()
            previous_stamp = self.stamp.read_bytes()
            self.archive.write_bytes(b"archive-two")
            self.prepare()
            self.assertEqual(marker.read(), previous_marker)
            self.assertEqual(stamp.read(), previous_stamp)
            self.assertNotEqual(self.marker.read_bytes(), previous_marker)
            self.assertNotEqual(self.stamp.read_bytes(), previous_stamp)

    def test_publication_is_readable_under_restrictive_umask(self):
        previous_umask = os.umask(0o077)
        try:
            self.prepare()
        finally:
            os.umask(previous_umask)
        for path in (self.marker, self.stamp):
            with self.subTest(output=path.name):
                self.assertEqual(path.stat().st_mode & 0o444, 0o444)


SANITIZER_STATE = """
#if defined(__SANITIZE_ADDRESS__)
#define PROBE_SANITIZED 1
#elif defined(__has_feature)
#if __has_feature(address_sanitizer)
#define PROBE_SANITIZED 1
#else
#define PROBE_SANITIZED 0
#endif
#else
#define PROBE_SANITIZED 0
#endif
"""


def native_source(value):
    """Keep the native object independent of the project header's value."""
    return SANITIZER_STATE + f"""
int native_value(void) {{ return {value}; }}
int native_sanitized(void) {{ return PROBE_SANITIZED; }}
"""


def native_header(value):
    """Expose a header-only constant and a separate CGO sanitizer probe."""
    return SANITIZER_STATE + f"""
enum {{ HEADER_VALUE = {value} }};
int native_value(void);
int native_sanitized(void);
static inline int cgo_sanitized(void) {{ return PROBE_SANITIZED; }}
"""


class NativeProject:
    """Use the production Meson recipe with a tiny observable native API."""

    def __init__(self, directory, *, controlplane=False):
        self.root = directory / "project with spaces"
        self.root.mkdir()
        self.build = self.root / "build"
        self.archive = self.build / "lib/errors/liberrors.a"
        self.marker = self.root / "bindings/go/cerrors/zz_yanet_cdeps.c"
        self.stamp = self.build / "lib/errors/cerrors-go-cdeps.stamp"
        self.source = self.root / "lib/errors/errors.c"
        self.header = self.root / "lib/errors/errors.h"
        self.binary = self.root / "probe"
        self.sanitized = False
        self.env = self.clean_environment(directory)
        self.source.parent.mkdir(parents=True)
        self.marker.parent.mkdir(parents=True)
        (self.root / "scripts").mkdir()
        (self.root / "cmd/probe").mkdir(parents=True)
        shutil.copyfile(HELPER, self.root / "scripts/go_cdeps.py")
        shutil.copyfile(
            ROOT / "lib/errors/meson.build", self.source.parent / "meson.build"
        )
        (self.root / "meson.build").write_text(
            """project('go-cdeps-regression', 'c', meson_version: '>=0.61.0',
  default_options: ['buildtype=release'])
dataplane_only = false
yanet_c_args = []
yanet_link_args = []
yanet_rootdir = include_directories('.')
python3 = find_program('python3')
go_cdeps = [python3, files('scripts/go_cdeps.py'), '--root', meson.project_source_root()]
subdir('lib/errors')
"""
        )
        (self.root / "go.mod").write_text("module example.test/cdeps\n\ngo 1.23\n")
        (self.marker.parent / "cgo.go").write_text(
            """package cerrors
/*
#cgo LDFLAGS: "${SRCDIR}/../../../build/lib/errors/liberrors.a"
#include "../../../lib/errors/errors.h"
*/
import "C"

func Values() (int, int, int, int) {
    return int(C.native_value()), int(C.HEADER_VALUE),
        int(C.native_sanitized()), int(C.cgo_sanitized())
}
"""
        )
        (self.marker.parent / "nocgo.go").write_text(
            """//go:build !cgo

package cerrors

func Values() (int, int, int, int) { return -1, -1, -1, -1 }
"""
        )
        (self.root / "cmd/probe/main.go").write_text(
            """package main

import (
    "fmt"
    "example.test/cdeps/bindings/go/cerrors"
)

func main() {
    a, b, c, d := cerrors.Values()
    fmt.Printf("%d %d %d %d\\n", a, b, c, d)
}
"""
        )
        self.source.write_text(native_source(11))
        self.header.write_text(native_header(7))
        if controlplane:
            self.add_controlplane_fixture()
        run_command([TOOLS.meson, "setup", self.build], cwd=self.root, env=self.env)

    def add_controlplane_fixture(self):
        """Use the real director recipe while all unrelated inputs share one archive."""
        directory = self.root / "controlplane"
        (directory / "ynpb").mkdir(parents=True)
        (directory / "etc/yanet/controlplane.d").mkdir(parents=True)
        (directory / "cmd/yncp-director").mkdir(parents=True)
        shutil.copyfile(ROOT / "controlplane/meson.build", directory / "meson.build")
        shutil.copyfile(
            self.root / "cmd/probe/main.go", directory / "cmd/yncp-director/main.go"
        )
        (directory / "ynpb/meson.build").write_text("ynpb_gen = lib_errors\n")
        (directory / "etc/yanet/controlplane.d/default.yaml").write_text("{}\n")
        aliases = """
common_protoc_gen blackhole_protoc_gen decap_protoc_gen dscp_protoc_gen
forward_protoc_gen mirror_protoc_gen nat64_protoc_gen route_protoc_gen
route_mpls_protoc_gen pdump_protoc_gen ring_protoc_gen acl_protoc_gen
unrdup_protoc_gen trafgen_protoc_gen plain_protoc_gen vlan_protoc_gen
vxlan_protoc_gen filter_protoc_gen fwstate_protoc_gen l3b_protoc_gen
common_readiness_protoc_gen route_operator_protoc_gen
route_operator_readiness_protoc_gen lib_acl_cp lib_acl_dp lib_blackhole_cp
lib_blackhole_dp lib_l3b_cp lib_l3b_dp lib_decap_cp lib_decap_dp lib_dscp_cp
lib_dscp_dp lib_forward_cp lib_forward_dp lib_fwstate_cp lib_fwstate_dp
lib_mirror_cp lib_mirror_dp lib_nat64_cp lib_nat64_dp lib_pdump_cp lib_pdump_dp
lib_route_cp lib_route_dp lib_unrdup_cp lib_dev_trafgen_api lib_dev_trafgen_dp
lib_dev_vxlan_api lib_filter_compiler lib_agent_counters lib_counter_pattern
lib_ring_objects
""".split()
        with (self.root / "meson.build").open("a") as output:
            output.write(
                "go = find_program('go')\n"
                "yanet_version = '0.0.0'\n"
                "yanet_go_env = ['GOFLAGS=-x']\n"
                f"foreach dependency : {aliases!r}\n"
                "  set_variable(dependency, lib_errors)\n"
                "endforeach\n"
                "librure_targets = []\n"
                "subdir('controlplane')\n"
            )

    @staticmethod
    def clean_environment(directory):
        """Keep native and Go flags isolated from the enclosing build."""
        cache = run_command([TOOLS.go, "env", "GOCACHE"], cwd=directory).stdout.strip()
        env = os.environ.copy()
        flags = {
            "CC", "CXX", "AR", "LD", "CFLAGS", "CXXFLAGS", "CPPFLAGS",
            "LDFLAGS", "OBJCFLAGS", "OBJCXXFLAGS", "PKG_CONFIG",
            "LD_PRELOAD", "LD_LIBRARY_PATH", "ASAN_OPTIONS", "LSAN_OPTIONS",
            "MSAN_OPTIONS", "TSAN_OPTIONS", "UBSAN_OPTIONS",
        }
        for key in list(env):
            if key.startswith(("GO", "CGO")) or key in flags:
                env.pop(key)
        env.update(
            GOCACHE=cache,
            GOPATH=str(directory / "go-path"),
            GOWORK="off",
            GOTOOLCHAIN="local",
            GOENV="off",
            GOPROXY="off",
            GOSUMDB="off",
            CGO_ENABLED="1",
            CC=TOOLS.cc,
            AR=TOOLS.ar,
            ASAN_OPTIONS="detect_leaks=0",
        )
        env["PATH"] = os.pathsep.join(
            [str(Path(TOOLS.meson).parent), str(Path(TOOLS.go).parent), env["PATH"]]
        )
        return env

    def prepare(self, *targets):
        """Run the default or requested target's native dependency graph."""
        return run_command(
            [TOOLS.meson, "compile", "-C", self.build, *targets],
            cwd=self.root,
            env=self.env,
        )

    def restore_baseline(self):
        """Rebuild a known release archive before each independent scenario."""
        self.set_sanitized(False)
        self.source.write_text(native_source(11))
        self.header.write_text(native_header(7))
        self.prepare()

    def set_sanitized(self, enabled):
        """Apply matching native and CGO sanitizer modes without changing APIs."""
        if self.sanitized == enabled:
            return
        run_command(
            [
                TOOLS.meson, "configure", self.build,
                "-Db_sanitize=" + ("address" if enabled else "none"),
            ],
            cwd=self.root,
            env=self.env,
        )
        self.sanitized = enabled

    def go_build(self, *, cgo=True):
        """Build the same output with ordinary Go commands and return its trace."""
        env = self.env.copy()
        env["CGO_ENABLED"] = "1" if cgo else "0"
        if self.sanitized and cgo:
            env["CGO_CFLAGS"] = "-O2 -g -fsanitize=address -fno-omit-frame-pointer"
            env["CGO_LDFLAGS"] = "-fsanitize=address"
        return run_command(
            [
                TOOLS.go, "build", "-x",
                "-o", self.binary, "./cmd/probe",
            ],
            cwd=self.root,
            env=env,
        ).stdout

    def values(self, binary=None):
        """Execute the linked binary so stale native behavior remains observable."""
        result = run_command([binary or self.binary], cwd=self.root, env=self.env)
        return tuple(map(int, result.stdout.split()))

    def replace_archive_value(self, value, *, preserve_mtime=False):
        """Replace only archive bytes while all Meson source inputs stay unchanged."""
        stat = self.archive.stat()
        members = run_command(
            [TOOLS.ar, "t", self.archive], cwd=self.root, env=self.env
        ).stdout.splitlines()
        if len(members) != 1:
            raise AssertionError(f"Expected one native object, got {members!r}")
        with tempfile.TemporaryDirectory(prefix="replacement ", dir=self.root) as name:
            replacement = Path(name)
            source = replacement / "replacement.c"
            obj = replacement / Path(members[0]).name
            source.write_text(native_source(value))
            run_command(
                [TOOLS.cc, "-O2", "-c", source, "-o", obj],
                cwd=self.root,
                env=self.env,
            )
            archive = replacement / "liberrors.a"
            run_command([TOOLS.ar, "rcs", archive, obj], cwd=self.root, env=self.env)
            os.replace(archive, self.archive)
        if preserve_mtime:
            os.utime(self.archive, ns=(stat.st_atime_ns, stat.st_mtime_ns))


class MesonGoCacheTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory(prefix="yanet cdeps integration ")
        cls.addClassCleanup(cls.temporary.cleanup)
        cls.project = NativeProject(Path(cls.temporary.name))
        cls.project.prepare()
        trace = cls.project.go_build()
        if not any(
            "example.test/cdeps/bindings/go/cerrors" in shlex.split(command)
            for command in go_tool_commands(trace, {"cgo"})
        ):
            raise AssertionError(f"Fresh fixture reused cached CGO compilation:\n{trace}")

    def setUp(self):
        self.project.restore_baseline()
        self.project.go_build()
        self.assertEqual(self.project.values(), (11, 7, 0, 0))

    def assert_cached_build(self):
        """Require settled Go actions, including the final executable link."""
        trace = self.project.go_build()
        self.assertEqual(go_tool_commands(trace), [], trace)

    def test_changed_archive_changes_native_behavior_with_warm_go_cache(self):
        self.project.replace_archive_value(22)
        self.project.prepare()
        trace = self.project.go_build()
        self.assertTrue(go_tool_commands(trace), trace)
        self.assertEqual(self.project.values(), (22, 7, 0, 0))

    def test_controlplane_target_prepares_native_fingerprint_before_go_build(self):
        with tempfile.TemporaryDirectory(prefix="yanet director cdeps ") as name:
            project = NativeProject(Path(name), controlplane=True)
            trace = project.prepare().stdout
            self.assertTrue(
                any(
                    "example.test/cdeps/bindings/go/cerrors" in shlex.split(command)
                    for command in go_tool_commands(trace, {"cgo"})
                ),
                trace,
            )
            project.go_build()
            # Settle the executable after every generated source is present.
            project.prepare("yanet-controlplane")
            director = project.build / "controlplane/yanet-controlplane"
            self.assertEqual(project.values(director), (11, 7, 0, 0))
            project.source.write_text(native_source(55))
            project.prepare("yanet-controlplane")
            self.assertEqual(project.values(director), (55, 7, 0, 0))

    def test_meson_native_source_change_invalidates_go_cache(self):
        archive = self.project.archive.read_bytes()
        self.project.source.write_text(native_source(44))
        self.project.prepare()
        if archive.startswith(b"!<thin>\n"):
            self.assertEqual(self.project.archive.read_bytes(), archive)
        self.project.go_build()
        self.assertEqual(self.project.values(), (44, 7, 0, 0))

    def test_changed_archive_with_preserved_mtime_invalidates_go_cache(self):
        previous_mtime = self.project.archive.stat().st_mtime_ns
        self.project.replace_archive_value(33, preserve_mtime=True)
        self.assertEqual(self.project.archive.stat().st_mtime_ns, previous_mtime)
        self.project.prepare()
        self.assertEqual(self.project.archive.stat().st_mtime_ns, previous_mtime)
        self.project.go_build()
        self.assertEqual(self.project.values(), (33, 7, 0, 0))

    def test_header_only_change_invalidates_cgo_with_identical_archive(self):
        archive = self.project.archive.read_bytes()
        self.project.header.write_text(native_header(9))
        self.project.prepare()
        self.assertEqual(self.project.archive.read_bytes(), archive)
        self.project.go_build()
        self.assertEqual(self.project.values(), (11, 9, 0, 0))

    def test_unchanged_preparation_preserves_marker_and_reuses_all_go_actions(self):
        marker = file_state(self.project.marker)
        stamp = file_state(self.project.stamp)
        self.project.prepare()
        self.assertEqual(file_state(self.project.marker), marker)
        self.assertEqual(file_state(self.project.stamp), stamp)
        self.assert_cached_build()

    def test_default_meson_build_restores_deleted_marker(self):
        marker = self.project.marker.read_bytes()
        self.project.marker.unlink()
        self.project.prepare()
        self.assertEqual(self.project.marker.read_bytes(), marker)
        self.assert_cached_build()

    def test_disabled_cgo_ignores_generated_c_marker(self):
        self.assertTrue(self.project.marker.is_file())
        trace = self.project.go_build(cgo=False)
        self.assertEqual(
            go_tool_commands(
                trace, {"cgo", "cc", "gcc", "clang", Path(TOOLS.cc).name}
            ),
            [], trace,
        )
        self.assertEqual(self.project.values(), (-1, -1, -1, -1))

    def test_release_asan_release_switches_match_flags_and_settle_each_time(self):
        for sanitized in (False, True, False):
            with self.subTest(sanitized=sanitized):
                self.project.set_sanitized(sanitized)
                self.project.prepare()
                self.project.go_build()
                expected = 1 if sanitized else 0
                self.assertEqual(self.project.values(), (11, 7, expected, expected))
                marker = file_state(self.project.marker)
                self.project.prepare()
                self.assertEqual(file_state(self.project.marker), marker)
                self.assert_cached_build()


def main():
    """Resolve tool paths before handing test selection to the standard runner."""
    parser = argparse.ArgumentParser(description=__doc__)
    for name, default in (("meson", "meson"), ("go", "go"), ("cc", "cc"), ("ar", "ar")):
        parser.add_argument("--" + name, default=default)
    global TOOLS
    TOOLS, remaining = parser.parse_known_args()
    for name in ("meson", "go", "cc", "ar"):
        value = getattr(TOOLS, name)
        resolved = shutil.which(value)
        if resolved is None:
            parser.error(f"{name} executable not found: {value}")
        setattr(TOOLS, name, str(Path(resolved).absolute()))
    unittest.main(argv=[sys.argv[0], *remaining])


if __name__ == "__main__":
    main()
