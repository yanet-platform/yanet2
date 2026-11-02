#!/usr/bin/env python3
"""Check full-suite recipe ordering with recording tools, without product builds."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
PACKAGES = ["recipe.invalid/unit", "recipe.invalid/other", "github.com/yanet-platform/yanet2/tests/functional/main"]
STUB = r'''import json, os, pathlib, subprocess, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["RECIPE_LOG"], "a") as log:
    log.write(json.dumps([name, args, os.environ.get("CGO_CPPFLAGS"), os.environ.get("GOFLAGS")]) + "\n")
operation = args[0] if args else ""
if name == "meson" and operation == "compile":
    operation = " ".join(args)
if name + ":" + operation == os.environ.get("RECIPE_FAIL"):
    sys.exit(31)
if name == "docker":
    sys.exit(subprocess.call(args[args.index("sh"):]))
if name == "go" and args[:1] == ["list"]:
    print(os.environ["RECIPE_PACKAGES"])
if name == "git" and "--git-common-dir" in args:
    print(os.environ["RECIPE_METADATA"])
if name == "git" and "--absolute-git-dir" in args:
    print(os.environ["RECIPE_METADATA"] + "/worktrees/fixture")
'''


class DockerTestRecipe(unittest.TestCase):
    def run_recipe(self, fail="", packages=PACKAGES, linked=False, recipe="dtest", extra=(), configured=True,
                   sdk_fixtures=False, make_packages=""):
        """Return the exit result and trace from a disposable checkout with harmless tools."""
        with tempfile.TemporaryDirectory(prefix="dtest-recipe-") as directory:
            root = Path(directory).resolve()
            for name in ("Makefile", "Justfile"):
                shutil.copyfile(ROOT / name, root / name)
            (root / "modules/private").mkdir(parents=True)
            (root / "modules/meson.build").write_text("")
            (root / "build").mkdir()
            (root / "build/yanet_cgo_cppflags").write_text("-DFIXTURE_CGO=1")
            if configured:
                (root / "build/build.ninja").write_text("build sdk-rust-fixtures: phony\n" if sdk_fixtures else "")
            if linked:
                (root / ".git").write_text("gitdir: fixture metadata\n")
            else:
                (root / ".git").mkdir()
            (root / "scripts").mkdir()
            (root / "scripts/verify-docker-ccache.sh").write_text("exit 0\n")
            binaries = root / "bin"
            binaries.mkdir()
            for name in ("docker", "git", "go", "meson", "python3", "cargo"):
                path = binaries / name
                path.write_text("#!" + sys.executable + "\n" + STUB)
                path.chmod(0o755)
            log = root / "commands.jsonl"
            environment = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                               HOME=str(root), RECIPE_LOG=str(log), RECIPE_FAIL=fail,
                               RECIPE_PACKAGES="\n".join(packages), RECIPE_METADATA=str(root / "metadata"),
                               DOCKER_CACHE_DIR=str(root / "cache with spaces"),
                               CGO_CPPFLAGS="-DINHERITED=1", GOFLAGS="-tags=fixture", GO_TEST_PACKAGES=make_packages)
            for name in ("MAKEFLAGS", "MFLAGS", "MAKELEVEL", "GO_TEST_FLAGS"):
                environment.pop(name, None)
            result = subprocess.run(["just", "--justfile", str(root / "Justfile"), recipe, *extra],
                                    env=environment, text=True, capture_output=True, timeout=30)
            commands = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
            return result, commands, str(root)

    def test_full_go_suite_precedes_meson_and_uses_build_flags(self):
        result, commands, _ = self.run_recipe()
        self.assertEqual(result.returncode, 0, result.stderr)
        go = [(args, cpp, flags) for name, args, cpp, flags in commands if name == "go"]
        self.assertEqual(go, [(["list", "-f", "{{.ImportPath}}", "-json=false", "./..."], "-DINHERITED=1 -DFIXTURE_CGO=1", "-tags=fixture"),
                              (["test", "-count=1", *PACKAGES[:-1]], "-DINHERITED=1 -DFIXTURE_CGO=1", "-tags=fixture")])
        events = [(name, args) for name, args, _, _ in commands if name in ("go", "meson")]
        self.assertLess(events.index(("meson", ["compile", "-C", "build"])), events.index(("go", go[0][0])))
        self.assertLess(events.index(("go", go[1][0])), events.index(("meson", ["test", "-C", "build"])))

    def test_fresh_and_existing_setup_keep_private_modules_and_reset_build_modes(self):
        for configured in (False, True):
            with self.subTest(configured=configured):
                result, commands, _ = self.run_recipe(configured=configured)
                self.assertEqual(result.returncode, 0, result.stderr)
                action = "configure" if configured else "setup"
                setup = next(args for name, args, _, _ in commands if name == "meson" and args[0] == action)
                for option in ("-Dextra_modules=private", "-Dbuildtype=debug", "-Db_sanitize=none",
                               "-Dfuzzing=disabled", "-Ddataplane_only=false", "-Db_coverage=false"):
                    self.assertIn(option, setup)
                if configured:
                    self.assertTrue(any(name == "meson" and args[:2] == ["setup", "--reconfigure"] for name, args, _, _ in commands))

    def test_docker_keeps_canonical_paths_narrow_trust_and_cache_mounts(self):
        for linked in (False, True):
            with self.subTest(linked=linked):
                result, commands, root = self.run_recipe(linked=linked)
                self.assertEqual(result.returncode, 0, result.stderr)
                docker = next(args for name, args, _, _ in commands if name == "docker")
                mounts = [docker[i + 1] for i, arg in enumerate(docker) if arg == "-v"]
                self.assertIn(root + ":/yanet2", mounts)
                self.assertIn(root + ":" + root, mounts)
                for name, target in (("gomodcache", "/tmp/gomodcache"), ("gocache", "/tmp/gocache"), ("ccache", "/ccache")):
                    self.assertIn(root + "/cache with spaces/" + name + ":" + target + ":rw", mounts)
                if linked:
                    self.assertIn(root + "/metadata:" + root + "/metadata:ro", mounts)
                    self.assertTrue(any(":/yanet2/.git:ro" in mount for mount in mounts))
                trust = [args[-1] for name, args, _, _ in commands if name == "git" and args[:1] == ["config"]]
                self.assertEqual(trust, ["/yanet2", root])

    def test_failures_stop_before_following_test_stages(self):
        for fail in ("python3:scripts/test-meson-ccache.py", "meson:configure", "meson:compile -C build",
                     "meson:compile -C build go-test-prepare", "go:list", "go:test", "meson:test"):
            with self.subTest(fail=fail):
                result, commands, _ = self.run_recipe(fail=fail)
                self.assertNotEqual(result.returncode, 0)
                events = [(name, args[0]) for name, args, _, _ in commands if args]
                if fail not in ("go:test", "meson:test"):
                    self.assertNotIn(("go", "test"), events)
                if fail != "meson:test":
                    self.assertNotIn(("meson", "test"), events)
                if fail == "meson:compile -C build":
                    self.assertEqual([args for name, args, _, _ in commands if name == "meson" and args[0] == "compile"],
                                     [["compile", "-C", "build"]])

    def test_empty_or_only_vm_packages_fail_without_running_tests(self):
        for packages in ([], [PACKAGES[-1]]):
            with self.subTest(packages=packages):
                result, commands, _ = self.run_recipe(packages=packages)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(name in ("go", "meson") and args[0] == "test" for name, args, _, _ in commands))

    def test_targeted_arguments_are_not_part_of_dtest(self):
        result, commands, _ = self.run_recipe(extra=("-run=TestOne",))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(name == "docker" for name, _, _, _ in commands))

    def test_other_docker_helpers_keep_their_command(self):
        result, commands, _ = self.run_recipe(recipe="dcli")
        self.assertEqual(result.returncode, 0, result.stderr)
        cargo = next(args for name, args, _, _ in commands if name == "docker")
        self.assertEqual(cargo[-1], "make cli")

    def test_docker_runner_keeps_multiword_commands(self):
        result, _, _ = self.run_recipe(recipe="_docker_run", extra=("-it", "printf", "fixture-argument"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "fixture-argument")

    def test_full_suite_keeps_configured_sdk_fixture_tags(self):
        result, commands, _ = self.run_recipe(sdk_fixtures=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([args[1] for name, args, _, _ in commands if name == "go"],
                         ["-tags=sdk_rust_fixtures", "-tags=sdk_rust_fixtures"])

    def test_make_keeps_ci_package_selection_without_meson(self):
        result, commands, _ = self.run_recipe(recipe="_docker_run", extra=("-it", "make test-only"),
                                              sdk_fixtures=True, make_packages=PACKAGES[1])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([args for name, args, _, _ in commands if name == "go"],
                         [["test", "-tags=sdk_rust_fixtures", "-count=1", PACKAGES[1]]])
        self.assertFalse(any(name == "meson" and args[0] == "test" for name, args, _, _ in commands))

    def test_dtest_still_runs_full_suite_with_inherited_ci_selection(self):
        result, commands, _ = self.run_recipe(make_packages=PACKAGES[1])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(["test", "-count=1", *PACKAGES[:-1]], [args for name, args, _, _ in commands if name == "go"])
        self.assertTrue(any(name == "meson" and args[0] == "test" for name, args, _, _ in commands))


if __name__ == "__main__":
    unittest.main(verbosity=2)
