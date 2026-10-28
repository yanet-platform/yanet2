#!/usr/bin/env python3
"""Exercise recipe boundaries with stubs and owned Go modules, without product builds."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = []
ORDINARY_PREFLIGHT_EVIDENCE = []
BOUNDARY_EVIDENCE = []
STUB = r'''#!/usr/bin/env python3
import json, os, pathlib, subprocess, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["RECIPE_LOG"], "a") as log:
    log.write(json.dumps([name, args, os.environ.get("CGO_CPPFLAGS"), os.environ.get("GOFLAGS")]) + "\n")
if name == "docker":
    options = args[:args.index("sh")]
    mounts = [options[i + 1] for i, arg in enumerate(options) if arg == "-v"]
    destinations = [mount.split(":")[1] for mount in mounts]
    assert len(destinations) == len(set(destinations)), destinations
    mount = args[args.index("-v") + 1]
    assert mount == os.environ["RECIPE_ROOT"] + ":/yanet2"
    if os.environ["RECIPE_ROOT"] != "/yanet2":
        assert os.environ["RECIPE_ROOT"] + ":" + os.environ["RECIPE_ROOT"] in args
    if pathlib.Path(os.environ["RECIPE_ROOT"], ".git").is_file():
        assert any(arg.endswith(":ro") and ":/yanet2/.git:" in arg for arg in args)
        common = subprocess.check_output([os.environ["RECIPE_GIT"], "-C", os.environ["RECIPE_ROOT"], "rev-parse", "--path-format=absolute", "--git-common-dir"], text=True).strip()
        assert common + ":" + common + ":ro" in args
    index = args.index("sh")
    command = args[index:]
    assert command[4] == os.environ["RECIPE_ROOT"]
    if os.environ["RECIPE_EXECUTE_DOCKER"] == "false":
        sys.exit(0)
    command[4] = os.environ["RECIPE_FIXTURE"]
    sys.exit(subprocess.call(command))
if name == "git":
    if args[:1] == ["config"]:
        if os.environ["RECIPE_TRUST"] != "true":
            sys.exit(0)
    sys.exit(subprocess.call([os.environ["RECIPE_GIT"], *args]))
if name == "meson" and args[:1] in (["setup"], ["compile"]) and os.environ["RECIPE_TRUST"] == "true":
    print("TRUST_PROVENANCE", subprocess.check_output([os.environ["RECIPE_GIT"], "rev-parse", "--show-toplevel"], text=True).strip())
if name == "meson" and os.environ["RECIPE_REAL_MESON"] == "true":
    sys.exit(subprocess.call([os.environ["RECIPE_MESON"], *args]))
if name == "go" and args[:1] in (["list"], ["test"]) and os.environ["RECIPE_REAL_GO"]:
    if args[0] == "list" or os.environ["RECIPE_REAL_GO"] == "all":
        sys.exit(subprocess.call([os.environ["RECIPE_GO"], *args]))
    sys.exit(0)
if name == "go" and args[:1] == ["list"]:
    if os.environ.get("RECIPE_FAIL") == "list":
        sys.exit(31)
    print(os.environ.get("RECIPE_PACKAGES", "github.com/yanet-platform/yanet2/modules/acl/tests/functional\ngithub.com/yanet-platform/yanet2/modules/l3b/controlplane\ngithub.com/yanet-platform/yanet2/tests/functional/main"))
if name == "go" and args[:1] == ["test"] and os.environ.get("RECIPE_FAIL") == "go":
    sys.exit(32)
if name == "meson" and args[:1] == ["test"] and os.environ.get("RECIPE_FAIL") == "meson":
    sys.exit(33)
if name == "meson" and args[:1] == ["setup"] and os.environ.get("RECIPE_FAIL") == "setup":
    sys.exit(35)
if name == "go" and args[:1] == ["clean"] and os.environ.get("RECIPE_FAIL") == "clean":
    sys.exit(36)
if name == "meson" and args[:1] == ["compile"] and os.environ.get("RECIPE_FAIL") == "compile":
    sys.exit(37)
if name == "python3" and os.environ.get("RECIPE_FAIL") == "preflight":
    sys.exit(34)
'''


class DockerTestRecipe(unittest.TestCase):
    def run_recipe(self, flags="", packages="", fail="", recipe="dtest", positional=None,
                   cache=None, cache_override=None, listed_packages=None, goflags="-mod=mod",
                   execute=True, source_root=None, ordinary=False, remove_trust=False,
                   real_go="", fixture_files=None, persisted_flags=None,
                   assume_owner=False, source_trust=True, real_meson=False, leak_options=False,
                   initial_sanitize=False,
                    linked_source=False, alias_trust=True):
        """Return the recipe result and command trace with product commands stubbed."""
        with tempfile.TemporaryDirectory(prefix="dtest-recipe-") as directory:
            temporary = Path(directory).resolve()
            fixture = temporary / "fixture"
            fixture.mkdir()
            shutil.copyfile(ROOT / "Makefile", fixture / "Makefile")
            shutil.copyfile(ROOT / "Justfile", fixture / "Justfile")
            justfile = fixture / "Justfile"
            (fixture / "modules").mkdir()
            (fixture / "modules/meson.build").write_text("subdir('route')\n")
            if fixture_files:
                for name, text in fixture_files.items():
                    path = fixture / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text(text.replace("@FIXTURE@", str(fixture)))
            justfile.write_text(justfile.read_text().replace(
                '@bash "{{ justfile_directory() }}/scripts/verify-docker-ccache.sh" "{{ justfile() }}"',
                '@python3 scripts/verify-docker-ccache.py').replace(
                'ROOT_DIR := justfile_directory()',
                'ROOT_DIR := env_var_or_default("RECIPE_ROOT", justfile_directory())'))
            if remove_trust:
                path = fixture / "Justfile"
                path.write_text(path.read_text().replace(' && git config --global --add safe.directory "$1"', ""))
            if leak_options:
                path = fixture / "Makefile"
                path.write_text(path.read_text().replace("\tmeson test -C build\n", "\tmeson test -C build $$GO_TEST_FLAGS\n"))
            config = fixture / "build/subprojects/dpdk/rte_build_config.h"
            config.parent.mkdir(parents=True)
            config.write_text("#define RTE_CACHE_LINE_SIZE 64\n")
            (fixture / "build/yanet_cgo_cppflags").write_text("-DYANET_CACHE_LINE_SIZE=64\n")
            (fixture / "glob-match").touch()
            binaries = temporary / "bin"
            binaries.mkdir()
            for name in ("docker", "go", "meson", "git", "python3"):
                stub = binaries / name
                stub.write_text(STUB.replace("#!/usr/bin/env python3", "#!" + sys.executable, 1))
                stub.chmod(0o755)
            log = temporary / "commands.jsonl"
            environment = os.environ.copy()
            environment.pop("MAKEFLAGS", None)
            environment.pop("MFLAGS", None)
            environment.pop("MAKELEVEL", None)
            environment.pop("DOCKER_CACHE_DIR", None)
            environment.pop("MESON_SETUP_FLAGS", None)
            environment.pop("GOFLAGS", None)
            environment.pop("RECIPE_PACKAGES", None)
            environment.pop("GIT_TEST_ASSUME_DIFFERENT_OWNER", None)
            for name in ("GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE"):
                environment.pop(name, None)
            home = temporary / "home"
            home.mkdir()
            if cache is not None:
                environment["DOCKER_CACHE_DIR"] = cache
            if listed_packages is not None:
                environment["RECIPE_PACKAGES"] = listed_packages
            if goflags is not None:
                environment["GOFLAGS"] = goflags
            environment.update(
                PATH=str(binaries) + os.pathsep + environment["PATH"],
                RECIPE_LOG=str(log),
                RECIPE_ROOT=str(ROOT),
                RECIPE_FIXTURE=str(fixture),
                RECIPE_GIT=shutil.which("git"),
                RECIPE_GO=shutil.which("go"),
                RECIPE_MESON=shutil.which("meson"),
                RECIPE_REAL_GO=real_go,
                RECIPE_REAL_MESON="true" if real_meson else "false",
                RECIPE_TRUST="true" if ordinary else "false",
                RECIPE_FAIL=fail,
                RECIPE_EXECUTE_DOCKER="true" if execute else "false",
                CGO_CPPFLAGS="-DTEST_INHERITED=1",
                HOME=str(home),
                GIT_CONFIG_GLOBAL=str(home / ".gitconfig"),
                GIT_CONFIG_NOSYSTEM="1",
                GIT_CONFIG_COUNT="0",
                GOWORK="off",
                GOENV=str(temporary / "goenv"),
                RECIPE_C_TEST_LOG=str(temporary / "c-tests.log"),
                RECIPE_C_FAIL="beta" if fail == "c-test" else "",
            )
            metadata = None
            trusted_source = ROOT
            if linked_source:
                assert not ordinary
                metadata = temporary / "source-metadata"
                subprocess.run([environment["RECIPE_GIT"], "init", "-q", "--separate-git-dir",
                                str(metadata), str(fixture)], cwd=fixture, env=environment, check=True)
                assert (fixture / ".git").is_file() and metadata.is_dir()
                source_root = str(fixture)
                trusted_source = fixture
            if assume_owner:
                environment["GIT_TEST_ASSUME_DIFFERENT_OWNER"] = "1"
            if source_trust:
                trusted_sources = [str(trusted_source)]
                if source_root == "/yanet2" and alias_trust and str(trusted_source) != "/yanet2":
                    trusted_sources.append("/yanet2")
                for source in trusted_sources:
                    subprocess.run([environment["RECIPE_GIT"], "config", "--global", "--add",
                                    "safe.directory", source], env=environment, check=True)
            trust = subprocess.run([environment["RECIPE_GIT"], "config", "--global", "--get-all",
                                    "safe.directory"], env=environment, capture_output=True, text=True)
            if trust.returncode not in (0, 1):
                trust.check_returncode()
            pretrusted = trust.stdout.splitlines()
            if persisted_flags is not None:
                Path(environment["GOENV"]).write_text("GOFLAGS=" + persisted_flags + "\n")
            ownership = {"trigger": None, "euid": os.geteuid()}
            if ordinary:
                subprocess.run([environment["RECIPE_GIT"], "init", "-q", str(fixture)], env=environment, check=True)
                owner = 65534 if os.geteuid() != 65534 else 65533
                try:
                    if not callable(getattr(os, "chown", None)):
                        raise NotImplementedError("ownership changes unavailable")
                    os.chown(fixture, owner, owner)
                    os.chown(fixture / ".git", owner, owner)
                except (PermissionError, NotImplementedError) as error:
                    environment["GIT_TEST_ASSUME_DIFFERENT_OWNER"] = "1"
                    ownership.update(trigger="git-assumption", capability_error=str(error))
                else:
                    ownership["trigger"] = "chown"
                    assert fixture.stat().st_uid != os.geteuid() and (fixture / ".git").stat().st_uid != os.geteuid()
                ownership.update(source_uid=fixture.stat().st_uid, metadata_uid=(fixture / ".git").stat().st_uid)
            if ordinary or remove_trust:
                source_root = str(fixture)
            if source_root is not None:
                environment["RECIPE_ROOT"] = source_root
            if initial_sanitize:
                subprocess.run([environment["RECIPE_MESON"], "setup", "-Db_sanitize=address,undefined", "build"],
                               cwd=fixture, env=environment, check=True, capture_output=True, text=True)
            arguments = ["just", "--justfile", str(justfile)]
            arguments.extend(["--set", "ROOT_DIR", source_root or str(ROOT)])
            if cache_override is not None:
                arguments.extend(["--set", "DOCKER_CACHE_DIR", cache_override])
            arguments.append(recipe)
            if recipe == "dtest":
                arguments.extend([flags, packages] if positional is None else positional)
            elif positional is not None:
                arguments.extend(positional)
            alias_lookup = None
            if source_root == "/yanet2" and assume_owner:
                result = subprocess.run([environment["RECIPE_GIT"], "-C", source_root, "rev-parse",
                                         "--path-format=absolute", "--git-common-dir"],
                                        env=environment, capture_output=True, text=True, timeout=30)
                alias_lookup = {"exit": result.returncode, "stdout": result.stdout, "stderr": result.stderr}
            if alias_lookup is None or alias_lookup["exit"] == 0:
                result = subprocess.run(arguments, env=environment, capture_output=True, text=True)
            commands = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
            test_log = fixture / "build/meson-logs/testlog.json"
            meson_tests = [json.loads(line) for line in test_log.read_text().splitlines()] if test_log.exists() else []
            c_log = Path(environment["RECIPE_C_TEST_LOG"])
            options_file = fixture / "build/meson-info/intro-buildoptions.json"
            meson_options = {option["name"]: option["value"] for option in json.loads(options_file.read_text())} if options_file.exists() else {}
            compile_file = fixture / "build/compile_commands.json"
            compile_commands = json.loads(compile_file.read_text()) if compile_file.exists() else []
            meson_archives = [str(path.relative_to(fixture)) for path in sorted((fixture / "build/modules").glob("*/*.a"))]
            EVIDENCE.append({"flags": flags, "packages": packages, "recipe": recipe,
                             "source_root": source_root, "ordinary": ordinary,
                             "remove_trust": remove_trust, "real_go": real_go,
                             "assume_owner": assume_owner, "source_trust": source_trust,
                             "alias_trust": alias_trust, "alias_lookup": alias_lookup,
                             "gowork": environment["GOWORK"],
                             "linked_source": linked_source, "source_gitfile": (fixture / ".git").is_file(),
                             "source_metadata": str(metadata) if metadata else None,
                             "pretrusted_directories": pretrusted, "ownership": ownership,
                             "real_meson": real_meson, "leak_options": leak_options,
                             "meson_tests": meson_tests,
                             "meson_options": meson_options, "meson_archives": meson_archives,
                             "compile_commands": compile_commands,
                             "c_test_events": c_log.read_text().splitlines() if c_log.exists() else [],
                             "exit": result.returncode, "stdout": result.stdout,
                             "stderr": result.stderr, "commands": commands})
            return result, commands

    def context_files(self):
        """Return a dependency-local Go module whose package membership exposes build context."""
        module = "module recipe.invalid/context\n\ngo 1.27.1\nrequire recipe.invalid/dependency v0.0.0\nreplace recipe.invalid/dependency => ./dependency-default\n"
        files = {"go.mod": module,
                 "alternate.mod": module.replace("dependency-default", "dependency-alternate"),
                 "dependency-default/go.mod": "module recipe.invalid/dependency\n\ngo 1.27.1\n",
                 "dependency-default/value.go": 'package dependency\nconst Value = "default"\n',
                 "dependency-alternate/go.mod": "module recipe.invalid/dependency\n\ngo 1.27.1\n",
                 "dependency-alternate/value.go": 'package dependency\nconst Value = "alternate"\n'}
        for name, tag in (("root", ""), ("plain", ""), ("custom", "custom"),
                          ("raceonly", "race"), ("msanonly", "msan"),
                          ("asanonly", "asan"), ("gconly", "gc"),
                          ("gccgoonly", "gccgo"), ("modulefile", "modulefile"),
                          ("overlay", "never_enabled")):
            prefix = "" if name == "root" else name + "/"
            constraint = "//go:build " + tag + "\n\n" if tag else ""
            files[prefix + "value.go"] = constraint + "package fixture\nconst Value = 1\n"
            test = ("package fixture\nimport (\"flag\"; \"fmt\"; \"testing\")\n"
                     'var marker = flag.String("marker", "", "")\n'
                     'var binaryTags = flag.String("tags", "", "")\n'
                     'var binaryRace = flag.Bool("race", false, "")\n'
                     '// Test_Context_Selected verifies that the selected package executes with literal binary data.\n'
                    'func Test_Context_Selected(t *testing.T) { fmt.Println("FIXTURE_SELECTED ' + name + '", *marker) }\n')
            if name == "modulefile":
                test = test.replace('"testing")', '"testing"; "recipe.invalid/dependency")')
                test = test.replace('fmt.Println(', 'if dependency.Value != "alternate" { t.Fatal(dependency.Value) }; fmt.Println(')
            files[prefix + "value_test.go"] = constraint + test
        files[".backing/value.go"] = "package fixture\nconst Value = 1\n"
        files[".backing/value_test.go"] = files["overlay/value_test.go"].split("\n\n", 1)[1]
        files["overlay.json"] = json.dumps({"Replace": {
            "@FIXTURE@/overlay/value.go": "@FIXTURE@/.backing/value.go",
            "@FIXTURE@/overlay/value_test.go": "@FIXTURE@/.backing/value_test.go",
        }})
        return files

    def meson_files(self):
        """Return an owned C project with two registered cases and an observable failing case."""
        return {"meson_options.txt": "option('extra_modules', type: 'array', value: [])\n",
                "meson.build": "project('recipe-boundary', 'c')\n"
                               "boundary = executable('boundary', 'boundary.c')\n"
                               "alias_target('go-test-prepare', boundary)\n"
                               "test('boundary-alpha', boundary, args: ['alpha'])\n"
                               "test('boundary-beta', boundary, args: ['beta'])\n",
                "boundary.c": r'''#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int
main(int argc, char **argv) {
	if (argc != 2 || (strcmp(argv[1], "alpha") && strcmp(argv[1], "beta"))) {
		return 87;
	}
	const char *path = getenv("RECIPE_C_TEST_LOG");
	FILE *log = path ? fopen(path, "a") : NULL;
	if (!log) {
		return 88;
	}
	fprintf(log, "%s\n", argv[1]);
	fclose(log);
	printf("C_FIXTURE_CASE %s\n", argv[1]);
	const char *fail = getenv("RECIPE_C_FAIL");
	return fail && !strcmp(fail, argv[1]) ? 17 : 0;
}
'''}

    def optional_module_files(self):
        """Return two optional archives, a public directory, and a Go package requiring external C code."""
        files = self.meson_files() | {
            "go.mod": "module recipe.invalid/optional\n\ngo 1.27.1\n",
            "modules/route/meson.build": "message('public directory is not optional')\n",
        }
        files["meson.build"] += "foreach name : get_option('extra_modules')\n  subdir('modules/' + name)\nendforeach\n"
        for name in ("recipe-optional-alpha", "recipe-optional-beta"):
            symbol = name.replace("-", "_")
            files["modules/" + name + "/meson.build"] = "static_library('" + symbol + "', 'native.c')\n"
            files["modules/" + name + "/native.c"] = "int\n" + symbol + "(void) {\n\treturn 2906;\n}\n"
        files["modules/recipe-optional-alpha/controlplane/value.go"] = '''package fixture

/*
#cgo LDFLAGS: ${SRCDIR}/../../../build/modules/recipe-optional-alpha/librecipe_optional_alpha.a
int recipe_optional_alpha(void);
*/
import "C"

func Value() int { return int(C.recipe_optional_alpha()) }
'''
        files["modules/recipe-optional-alpha/controlplane/value_test.go"] = '''package fixture_test

import (
	"fmt"
	"testing"

	fixture "recipe.invalid/optional/modules/recipe-optional-alpha/controlplane"
)

// Test_OptionalArchive_Value verifies that the C archive supplies the Go result.
func Test_OptionalArchive_Value(t *testing.T) {
	if value := fixture.Value(); value != 2906 {
		t.Fatal(value)
	}
	fmt.Println("OPTIONAL_ARCHIVE_VALUE 2906")
}
'''
        return files

    def test_setup_forwards_only_optional_modules_and_debug_coverage(self):
        for optional in (False, True):
            for coverage in ("false", "true"):
                with self.subTest(optional=optional, coverage=coverage):
                    files = {"modules/route/meson.build": ""}
                    if optional:
                        files.update({"modules/recipe-optional-alpha/meson.build": "",
                                      "modules/recipe-optional-beta/meson.build": ""})
                    result, commands = self.run_recipe(recipe="dsetup", positional=[coverage], fixture_files=files)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    setup = next(args for name, args, _, _ in commands if name == "meson" and args[0] == "setup")
                    selected = "recipe-optional-alpha,recipe-optional-beta" if optional else ""
                    self.assertEqual(setup, ["setup", "-Dbuildtype=debug", "-Db_sanitize=none", "-Db_coverage=" + coverage,
                                             "-Dextra_modules=" + selected, "build"])

    def test_plain_make_setup_keeps_default_options(self):
        for optional in (False, True):
            with self.subTest(optional=optional):
                files = {"modules/route/meson.build": ""}
                if optional:
                    files.update({"modules/recipe-optional-alpha/meson.build": "",
                                  "modules/recipe-optional-beta/meson.build": ""})
                result, commands = self.run_recipe(recipe="_docker_run", positional=["-it", "make setup"], fixture_files=files)
                self.assertEqual(result.returncode, 0, result.stderr)
                setup = next(args for name, args, _, _ in commands if name == "meson" and args[0] == "setup")
                selected = "recipe-optional-alpha,recipe-optional-beta" if optional else ""
                self.assertEqual(setup, ["setup", "-Dextra_modules=" + selected, "build"])

    def test_existing_build_reconfigures_requested_coverage(self):
        result, commands = self.run_recipe(
            recipe="_docker_run", positional=["-it", "just setup false && just setup true"],
            real_meson=True, fixture_files=self.meson_files())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any("--coverage" in command["command"] for command in EVIDENCE[-1]["compile_commands"]))
        setups = [args for name, args, _, _ in commands if name == "meson" and args[0] == "setup"]
        self.assertEqual(len(setups), 2)
        self.assertIn("--reconfigure", setups[1])
        self.assertTrue(any(name == "meson" and args[0] == "configure" and "-Db_coverage=true" in args
                            for name, args, _, _ in commands))

    def test_dtest_clears_existing_sanitizers_before_go_and_c_tests(self):
        result, commands = self.run_recipe("-v", "./plain", real_go="all", real_meson=True,
                                           initial_sanitize=True,
                                           fixture_files=self.context_files() | self.meson_files())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(EVIDENCE[-1]["meson_options"]["b_sanitize"], "none")
        self.assertFalse(any("-fsanitize=" in command["command"] for command in EVIDENCE[-1]["compile_commands"]))
        self.assertIn("FIXTURE_SELECTED plain", result.stdout)
        self.assertCountEqual(EVIDENCE[-1]["c_test_events"], ["alpha", "beta"])
        self.assertTrue(any(name == "meson" and args[0] == "configure" and "-Db_sanitize=none" in args
                            for name, args, _, _ in commands))

    def test_optional_meson_archive_supplies_automatically_selected_go_package(self):
        for omitted in (False, True):
            with self.subTest(optional_setup_omitted=omitted):
                files = self.optional_module_files()
                if omitted:
                    files["Justfile"] = (ROOT / "Justfile").read_text().replace(
                        '    @make setup MESON_SETUP_FLAGS="-Dbuildtype=debug -Db_sanitize=none -Db_coverage={{ COVERAGE_MODE }}"',
                        "    @meson setup build -Dbuildtype=debug -Db_coverage={{ COVERAGE_MODE }}")
                result, commands = self.run_recipe("-v", real_go="all", real_meson=True, fixture_files=files)
                evidence = EVIDENCE[-1]
                self.assertEqual(result.returncode == 0, not omitted, result.stderr)
                self.assertEqual(evidence["meson_options"]["extra_modules"], [] if omitted else
                                 ["recipe-optional-alpha", "recipe-optional-beta"])
                self.assertEqual(evidence["meson_options"]["buildtype"], "debug")
                self.assertFalse(evidence["meson_options"]["b_coverage"])
                self.assertEqual(evidence["meson_options"]["optimization"], "0")
                self.assertEqual(evidence["meson_options"]["b_sanitize"], "none")
                operations = [(name, args[0]) for name, args, _, _ in commands if name in ("go", "meson")]
                expected = [("meson", "setup"), ("meson", "compile"), ("meson", "compile"), ("go", "list"), ("go", "test")]
                if omitted:
                    self.assertEqual(evidence["meson_archives"], [])
                    self.assertIn("librecipe_optional_alpha.a: No such file or directory", result.stderr)
                    self.assertEqual(operations, expected)
                    self.assertEqual(evidence["meson_tests"], [])
                    self.assertEqual(evidence["c_test_events"], [])
                else:
                    self.assertEqual(evidence["meson_archives"], [
                        "build/modules/recipe-optional-alpha/librecipe_optional_alpha.a",
                        "build/modules/recipe-optional-beta/librecipe_optional_beta.a",
                    ])
                    self.assertIn("OPTIONAL_ARCHIVE_VALUE 2906", result.stdout)
                    invocation = next(args for name, args, _, _ in commands if name == "go" and args[0] == "test")
                    self.assertEqual(invocation, ["test", "-count=1", "recipe.invalid/optional/modules/recipe-optional-alpha/controlplane", "-v"])
                    self.assertEqual(operations, expected + [("meson", "test")])
                    self.assertCountEqual(evidence["c_test_events"], ["alpha", "beta"])
                    self.assertEqual({test["name"] for test in evidence["meson_tests"]}, {"boundary-alpha", "boundary-beta"})
                    self.assertTrue(all(test["result"] == "OK" and test["returncode"] == 0 for test in evidence["meson_tests"]))

    def test_isolated_home_preserves_only_narrow_linked_source_trust(self):
        for trust in (True, False):
            with self.subTest(source_trust=trust):
                result, commands = self.run_recipe(assume_owner=True, source_trust=trust, linked_source=True)
                evidence = EVIDENCE[-1]
                self.assertEqual(result.returncode == 0, trust, result.stderr)
                self.assertTrue(evidence["source_gitfile"])
                self.assertFalse(Path(evidence["source_metadata"]).is_relative_to(Path(evidence["source_root"])))
                self.assertEqual(evidence["pretrusted_directories"], [evidence["source_root"]] if trust else [])
                if trust:
                    invocation = next(args for name, args, _, _ in commands if name == "docker")
                    self.assertIn(evidence["source_metadata"] + ":" + evidence["source_metadata"] + ":ro", invocation)
                else:
                    self.assertIn("dubious ownership", result.stderr)
                    self.assertFalse(any(name == "docker" for name, _, _, _ in commands))

    def test_preflight_fixtures_accept_ordinary_outer_checkout(self):
        marker = os.environ.get("RECIPE_ORDINARY_OUTER")
        if marker and Path(marker).resolve() == ROOT:
            # The owned child runs the complete suite once without nesting another copy.
            self.assertTrue((ROOT / ".git").is_dir())
            return
        with tempfile.TemporaryDirectory(prefix="dtest-ordinary-outer-") as directory:
            temporary = Path(directory).resolve()
            outer = temporary / "source"
            outer.mkdir()
            for name in ("Makefile", "Justfile", "tests/recipes/dtest_test.py"):
                path = outer / name
                path.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, path)
            home = temporary / "home"
            home.mkdir()
            environment = os.environ.copy()
            for name in ("MAKEFLAGS", "MFLAGS", "MAKELEVEL", "GIT_DIR", "GIT_COMMON_DIR",
                         "GIT_WORK_TREE", "GIT_TEST_ASSUME_DIFFERENT_OWNER"):
                environment.pop(name, None)
            environment.update(HOME=str(home), GIT_CONFIG_GLOBAL=str(home / ".gitconfig"),
                               GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_COUNT="0",
                               GOWORK="off", RECIPE_ORDINARY_OUTER=str(outer))
            subprocess.run([shutil.which("git"), "init", "-q", str(outer)],
                           cwd=outer, env=environment, check=True)
            self.assertTrue((outer / ".git").is_dir())
            result = subprocess.run(["make", "test-recipes"], cwd=outer, env=environment,
                                    capture_output=True, text=True)
            cases = [json.loads(line.split(" ", 1)[1]) for line in result.stdout.splitlines()
                     if line.startswith("RECIPE_EVIDENCE ")]
            runtime = [json.loads(line.split(" ", 1)[1]) for line in result.stdout.splitlines()
                       if line.startswith("RECIPE_RUNTIME ")]
            methods = unittest.defaultTestLoader.getTestCaseNames(type(self))
            ORDINARY_PREFLIGHT_EVIDENCE.append({"root": str(outer), "git_directory": True,
                                               "exit": result.returncode, "methods": methods,
                                               "runtime": runtime, "fixture_evidence": cases,
                                               "stdout": result.stdout, "stderr": result.stderr})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(runtime[0]["root"], str(outer))
            self.assertIn("Ran " + str(len(methods)) + " tests", result.stderr)
            self.assertTrue(result.stderr.rstrip().endswith("OK"), result.stderr)
            for method in methods:
                self.assertIn(method + " (__main__.DockerTestRecipe." + method + ") ... ok", result.stderr)

    def test_ownership_fixture_retains_negative_sensitivity_without_chown(self):
        for capability in ("permission denied", "unavailable"):
            for remove in (False, True):
                with self.subTest(capability=capability, remove_trust=remove):
                    patch = (mock.patch("os.chown", side_effect=PermissionError("fixture chown capability unavailable"))
                             if capability == "permission denied" else mock.patch("os.chown", None))
                    with patch:
                        result, commands = self.run_recipe(ordinary=True, remove_trust=remove)
                    self.assertEqual(result.returncode == 0, not remove, result.stderr)
                    self.assertEqual(EVIDENCE[-1]["ownership"]["trigger"], "git-assumption")
                    self.assertEqual(EVIDENCE[-1]["pretrusted_directories"], [str(ROOT)])
                    if remove:
                        self.assertIn("dubious ownership", result.stderr)
                        self.assertFalse(any(name == "go" for name, _, _, _ in commands))
                    else:
                        self.assertIn("TRUST_PROVENANCE", result.stdout)

    def test_symlinked_temporary_paths_keep_the_child_recursion_guard(self):
        with tempfile.TemporaryDirectory(prefix="dtest-symlink-control-") as directory:
            temporary = Path(directory).resolve()
            physical = temporary / "physical"
            physical.mkdir()
            logical = temporary / "logical"
            logical.symlink_to(physical, target_is_directory=True)
            outer = physical / "source"
            outer.mkdir()
            for name in ("Makefile", "Justfile", "tests/recipes/dtest_test.py"):
                path = outer / name
                path.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, path)
            home = temporary / "home"
            home.mkdir()
            environment = os.environ.copy()
            for name in ("GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_TEST_ASSUME_DIFFERENT_OWNER"):
                environment.pop(name, None)
            environment.update(HOME=str(home), GIT_CONFIG_GLOBAL=str(home / ".gitconfig"),
                               GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_COUNT="0", TMPDIR=str(logical),
                               RECIPE_ORDINARY_OUTER=str(logical / "source"))
            subprocess.run([shutil.which("git"), "init", "-q", str(outer)],
                           cwd=outer, env=environment, check=True)
            probe = r'''import importlib.util, json, os, sys, unittest
from unittest import mock
spec = importlib.util.spec_from_file_location("owned_recipe", sys.argv[1])
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)
suite = unittest.TestSuite([harness.DockerTestRecipe("test_preflight_fixtures_accept_ordinary_outer_checkout")])
with mock.patch.object(harness.tempfile, "TemporaryDirectory", side_effect=AssertionError("attempted another child suite")) as nesting:
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    print("BOUNDED_GUARD " + json.dumps({"root": str(harness.ROOT), "marker": os.environ["RECIPE_ORDINARY_OUTER"], "nesting_attempts": nesting.call_count}))
sys.exit(not result.wasSuccessful())
'''
            with self.subTest(name="logical child marker cannot start another suite"):
                result = subprocess.run([sys.executable, "-c", probe, str(outer / "tests/recipes/dtest_test.py")],
                                        cwd=outer, env=environment, capture_output=True, text=True, timeout=30)
                guard = json.loads(next(line.split(" ", 1)[1] for line in result.stdout.splitlines()
                                        if line.startswith("BOUNDED_GUARD ")))
                BOUNDARY_EVIDENCE.append({"kind": "symlinked recursion", "context": str(ROOT),
                                          "exit": result.returncode, "guard": guard,
                                          "stdout": result.stdout, "stderr": result.stderr})
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(guard["nesting_attempts"], 0)
                self.assertEqual(Path(guard["marker"]).resolve(), Path(guard["root"]))
            with self.subTest(name="recipe paths remain physical under symlinked temporary root"):
                with mock.patch.dict(os.environ, TMPDIR=str(logical)), mock.patch.object(tempfile, "tempdir", str(logical)):
                    result, _ = self.run_recipe("-tags=custom -v", real_go="all", linked_source=True,
                                                fixture_files=self.context_files())
                self.assertEqual(result.returncode, 0, result.stderr)
                source = Path(EVIDENCE[-1]["source_root"])
                self.assertEqual(source, source.resolve())
                self.assertIn("FIXTURE_SELECTED custom ", result.stdout)

    def test_owned_go_modules_ignore_an_unrelated_explicit_workspace(self):
        with tempfile.TemporaryDirectory(prefix="dtest-workspace-control-") as directory:
            temporary = Path(directory).resolve()
            module = temporary / "unrelated"
            module.mkdir()
            (module / "go.mod").write_text("module recipe.invalid/unrelated\n\ngo 1.27.1\n")
            workspace = temporary / "go.work"
            workspace.write_text("go 1.27.1\n\nuse ./unrelated\n")
            outside = temporary / "outside"
            outside.mkdir()
            (outside / "go.mod").write_text("module recipe.invalid/outside\n\ngo 1.27.1\n")
            (outside / "value.go").write_text("package outside\nconst Value = 1\n")
            environment = dict(os.environ, GOWORK=str(workspace))
            negative = subprocess.run([shutil.which("go"), "list", "./..."], cwd=outside,
                                      env=environment, capture_output=True, text=True, timeout=30)
            self.assertNotEqual(negative.returncode, 0)
            self.assertIn("go.work", negative.stderr)
            with mock.patch.dict(os.environ, GOWORK=str(workspace)):
                result, commands = self.run_recipe("-tags=custom -v", real_go="all",
                                                   fixture_files=self.context_files())
                self.assertEqual(os.environ["GOWORK"], str(workspace))
            BOUNDARY_EVIDENCE.append({"kind": "ambient workspace", "context": str(ROOT),
                                      "workspace": str(workspace), "negative_exit": negative.returncode,
                                      "negative_stderr": negative.stderr, "exit": result.returncode,
                                      "fixture_gowork": EVIDENCE[-1]["gowork"]})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(EVIDENCE[-1]["gowork"], "off")
            self.assertIn("FIXTURE_SELECTED custom ", result.stdout)
            self.assertTrue(any(name == "go" and args[0] == "list" for name, args, _, _ in commands))

    def check_alias_source_trust_precedes_real_metadata_lookup(self):
        for source_trust, alias_trust in ((True, True), (True, False), (False, False)):
            with self.subTest(source_trust=source_trust, alias_trust=alias_trust):
                result, commands = self.run_recipe(source_root="/yanet2", recipe="_docker_run",
                                                   positional=["-it", "go version"], execute=False,
                                                   assume_owner=True, source_trust=source_trust,
                                                   alias_trust=alias_trust)
                trusted = [str(ROOT)] if source_trust else []
                if source_trust and alias_trust and ROOT != Path("/yanet2"):
                    trusted.append("/yanet2")
                accepted = source_trust and (alias_trust or ROOT == Path("/yanet2"))
                self.assertEqual(result.returncode == 0, accepted, result.stderr)
                self.assertEqual(EVIDENCE[-1]["pretrusted_directories"], trusted)
                self.assertEqual(EVIDENCE[-1]["alias_lookup"]["exit"] == 0, accepted)
                if accepted:
                    self.assertTrue(any(name == "docker" for name, _, _, _ in commands))
                else:
                    self.assertIn("dubious ownership", result.stderr)
                    self.assertFalse(any(name == "docker" for name, _, _, _ in commands))

    def test_alias_trust_handles_owned_distinct_and_coincident_source_roots(self):
        for coincident in (False, True):
            with self.subTest(coincident=coincident), tempfile.TemporaryDirectory(prefix="dtest-alias-topology-") as directory:
                temporary = Path(directory).resolve()
                alias = temporary / "alias"
                canonical = alias if coincident else temporary / "canonical"
                alias.mkdir()
                if not coincident:
                    canonical.mkdir()
                home = temporary / "home"
                home.mkdir()
                environment = os.environ.copy()
                for name in ("GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_TEST_ASSUME_DIFFERENT_OWNER"):
                    environment.pop(name, None)
                environment.update(HOME=str(home), GIT_CONFIG_GLOBAL=str(home / ".gitconfig"),
                                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_COUNT="0")
                metadata = temporary / "metadata"
                subprocess.run([shutil.which("git"), "init", "-q", "--separate-git-dir", str(metadata), str(alias)],
                               cwd=alias, env=environment, check=True)
                if not coincident:
                    shutil.copyfile(alias / ".git", canonical / ".git")
                shutil.copyfile(ROOT / "Makefile", canonical / "Makefile")
                (canonical / "Justfile").write_text((ROOT / "Justfile").read_text().replace("/yanet2", str(alias)))
                source = (ROOT / "tests/recipes/dtest_test.py").read_text()
                for literal in ("/yanet2", ":/yanet2", ":/yanet2/.git:"):
                    source = source.replace(json.dumps(literal), json.dumps(literal.replace("/yanet2", str(alias))))
                path = canonical / "tests/recipes/dtest_test.py"
                path.parent.mkdir(parents=True)
                path.write_text(source)
                probe = r'''import importlib.util, json, sys, unittest
spec = importlib.util.spec_from_file_location("owned_recipe", sys.argv[1])
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)
suite = unittest.TestSuite([harness.DockerTestRecipe("check_alias_source_trust_precedes_real_metadata_lookup")])
result = unittest.TextTestRunner(verbosity=2).run(suite)
print("BOUNDED_ALIAS_EVIDENCE " + json.dumps({"root": str(harness.ROOT), "alias": sys.argv[2], "cases": harness.EVIDENCE}))
sys.exit(not result.wasSuccessful())
'''
                result = subprocess.run([sys.executable, "-c", probe, str(path), str(alias)], cwd=canonical,
                                        env=environment, capture_output=True, text=True, timeout=30)
                evidence = json.loads(next(line.split(" ", 1)[1] for line in result.stdout.splitlines()
                                           if line.startswith("BOUNDED_ALIAS_EVIDENCE ")))
                BOUNDARY_EVIDENCE.append({"kind": "alias topology", "context": str(ROOT),
                                          "coincident": coincident, "metadata": str(metadata),
                                          "exit": result.returncode, "evidence": evidence,
                                          "stdout": result.stdout, "stderr": result.stderr})
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(evidence["root"] == evidence["alias"], coincident)
                self.assertEqual(len(evidence["cases"]), 3)
                self.assertEqual({(case["source_trust"], case["alias_trust"]) for case in evidence["cases"]},
                                 {(True, True), (True, False), (False, False)})
                self.assertTrue((canonical / ".git").is_file() and (alias / ".git").is_file())

    def test_package_field_rejects_options_before_go_test(self):
        for packages in ("./plain -run=^$", "./plain -args -marker hidden", "-race", "./plain --", "./plain -"):
            with self.subTest(packages=packages):
                result, commands = self.run_recipe(packages=packages)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(name == "go" and args[0] == "test" for name, args, _, _ in commands))
                self.assertFalse(any(name == "meson" and args[0] == "test" for name, args, _, _ in commands))
        result, commands = self.run_recipe("-run=^$", "./plain ./... ./-valid-pattern recipe.invalid/pkg")
        self.assertEqual(result.returncode, 0, result.stderr)
        invocation = next(args for name, args, _, _ in commands if name == "go" and args[0] == "test")
        self.assertEqual(invocation, ["test", "-count=1", "./plain", "./...", "./-valid-pattern", "recipe.invalid/pkg", "-run=^$"])

    def test_directory_flag_is_rejected_for_both_package_selection_modes(self):
        for packages in ("", "./plain"):
            for flags in ("-C subdir", "-v -C=subdir", "--C subdir", "--C=subdir"):
                with self.subTest(packages=packages, flags=flags):
                    result, commands = self.run_recipe(flags, packages)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("-C is not supported", result.stderr)
                    self.assertFalse(any(name == "go" and args[0] in ("list", "test") for name, args, _, _ in commands))

    def test_go_options_preserve_real_registered_c_tests_and_failures(self):
        flags = "-count=2 -run ^Test_Context_Selected$ -v"
        for name, options, fail, leak in (("default c selection", "", "", False),
                                          ("targeted go leaves c selection", flags, "", False),
                                          ("registered c failure propagates", flags, "c-test", False),
                                          ("leaked go options fail meson", flags, "", True)):
            with self.subTest(name=name):
                result, commands = self.run_recipe(options, "./plain", fail=fail, leak_options=leak,
                                                   real_go="all", real_meson=True,
                                                   fixture_files=self.context_files() | self.meson_files())
                evidence = EVIDENCE[-1]
                invocation = next(args for command, args, _, _ in commands if command == "meson" and args[0] == "test")
                self.assertEqual(result.returncode == 0, not (fail or leak), result.stderr)
                if leak:
                    self.assertEqual(invocation, ["test", "-C", "build", *options.split()])
                    self.assertEqual(evidence["c_test_events"], [])
                else:
                    self.assertEqual(invocation, ["test", "-C", "build"])
                    self.assertCountEqual(evidence["c_test_events"], ["alpha", "beta"])
                    tests = {test["command"][-1]: test for test in evidence["meson_tests"]}
                    self.assertEqual(set(tests), {"alpha", "beta"})
                    for case, test in tests.items():
                        self.assertEqual(test["result"], "FAIL" if fail and case == "beta" else "OK")
                        self.assertEqual(test["returncode"], 17 if fail and case == "beta" else 0)
                        self.assertIn("C_FIXTURE_CASE " + case, test["stdout"])

    def test_alias_root_has_unique_mount_destinations(self):
        result, commands = self.run_recipe(source_root="/yanet2", recipe="_docker_run",
                                           positional=["-it", "go version"], execute=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        args = next(args for name, args, _, _ in commands if name == "docker")
        mounts = [args[i + 1] for i, arg in enumerate(args) if arg == "-v"]
        self.assertEqual(mounts.count("/yanet2:/yanet2"), 1)
        self.assertIn("/tmp/gocache:/tmp/gocache:rw", mounts)

    def test_ordinary_checkout_requires_canonical_cwd_trust(self):
        for remove in (False, True):
            with self.subTest(remove_trust=remove):
                result, commands = self.run_recipe(ordinary=True, remove_trust=remove)
                self.assertEqual(result.returncode == 0, not remove, result.stderr)
                if not remove:
                    self.assertIn("TRUST_PROVENANCE", result.stdout)
                    self.assertEqual(EVIDENCE[-1]["pretrusted_directories"], [str(ROOT)])
                    canonical = next(args[4] for name, args, _, _ in commands
                                     if name == "git" and args[:4] == ["config", "--global", "--add", "safe.directory"]
                                     and args[4] != "/yanet2")
                    self.assertIn("TRUST_PROVENANCE " + canonical, result.stdout)
                else:
                    self.assertIn("dubious ownership", result.stderr)
                    self.assertFalse(any(name == "go" for name, _, _, _ in commands))

    def test_real_go_discovery_matches_per_call_build_context(self):
        for flags, package, real_go in (("-tags=custom -v", "custom", "all"),
                                        ("-race -v", "raceonly", "all"),
                                        ("-tags=modulefile -modfile=alternate.mod -v", "modulefile", "all"),
                                        ("-overlay=overlay.json -v", "overlay", "all"),
                                        ("--compiler=gc -v", "gconly", "all"),
                                        ("-compiler gccgo", "gccgoonly", "list"),
                                        ("-msan", "msanonly", "list"),
                                        ("-asan", "asanonly", "list")):
            with self.subTest(flags=flags):
                result, commands = self.run_recipe(flags, real_go=real_go, fixture_files=self.context_files())
                self.assertEqual(result.returncode, 0, result.stderr)
                if real_go == "all":
                    self.assertIn("FIXTURE_SELECTED " + package, result.stdout)
                invocation = next(args for name, args, _, _ in commands if name == "go" and args[0] == "test")
                self.assertIn("recipe.invalid/context/" + package, invocation)
                self.assertTrue(any(name == "meson" and args[0] == "test" for name, args, _, _ in commands))

    def test_real_go_explicit_context_overrides_inherited_and_persisted_defaults(self):
        for inherited, persisted, flags, selected, omitted in (
            ("-mod=mod -tags=custom", None, "-tags=other -v", "plain", "custom"),
            ("-mod=mod -race", None, "-race=false -v", "plain", "raceonly"),
            ("-mod=mod -compiler=gccgo", None, "-compiler=gc -v", "gconly", "gccgoonly"),
            (None, "-mod=mod -tags=custom", "-v", "custom", "raceonly"),
            ("-mod=mod -tags=custom -json", None, "-v", "custom", "raceonly"),
        ):
            with self.subTest(inherited=inherited, persisted=persisted, flags=flags):
                result, commands = self.run_recipe(flags, goflags=inherited, persisted_flags=persisted,
                                                   real_go="all", fixture_files=self.context_files())
                self.assertEqual(result.returncode, 0, result.stderr)
                invocation = next(args for name, args, _, _ in commands if name == "go" and args[0] == "test")
                self.assertIn("recipe.invalid/context/" + selected, invocation)
                self.assertNotIn("recipe.invalid/context/" + omitted, invocation)
                self.assertIn("FIXTURE_SELECTED " + selected, result.stdout)
                self.assertTrue(all(defaults == inherited for name, _, _, defaults in commands if name == "go"))

    def test_real_go_flag_like_test_values_do_not_change_discovery(self):
        for value in ("-race", "-args", "--"):
            with self.subTest(value=value):
                flags = "-run " + value + " --tags custom"
                result, commands = self.run_recipe(flags, real_go="list", fixture_files=self.context_files())
                self.assertEqual(result.returncode, 0, result.stderr)
                invocation = next(args for name, args, _, _ in commands if name == "go" and args[0] == "test")
                self.assertIn("recipe.invalid/context/custom", invocation)
                self.assertNotIn("recipe.invalid/context/raceonly", invocation)
                listing = next(args for name, args, _, _ in commands if name == "go" and args[0] == "list")
                self.assertEqual(listing, ["list", "-f", "{{.ImportPath}}", "-json=false", "-tags=custom", "./..."])

    def test_automatic_selection_projects_shared_build_options_without_test_flags(self):
        booleans = ("a", "asan", "buildvcs", "cover", "linkshared", "modcacherw",
                    "msan", "race", "trimpath", "work", "x")
        values = ("asmflags", "buildmode", "compiler", "covermode", "coverpkg", "gccgoflags",
                  "gcflags", "installsuffix", "ldflags", "mod", "modfile", "overlay", "p",
                  "pgo", "pkgdir", "tags", "toolexec")
        flags = ["--" + name + "=false" for name in booleans]
        flags.extend(word for name in values for word in ("-" + name, "a'b;$HOME$(exit_99)*"))
        flags.extend(("-v", "-test.short", "-run", "-race", "-test.count=2", "-coverprofile=profile.out",
                      "-args", "-tags=not_build_context"))
        result, commands = self.run_recipe(" ".join(flags))
        self.assertEqual(result.returncode, 0, result.stderr)
        listing = next(args for name, args, _, _ in commands if name == "go" and args[0] == "list")
        expected = ["-" + name + "=false" for name in booleans]
        expected.extend("-" + name + "=a'b;$HOME$(exit_99)*" for name in values)
        self.assertEqual(listing, ["list", "-f", "{{.ImportPath}}", "-json=false", *expected, "./..."])

    def test_automatic_selection_rejects_unknown_context_and_missing_values(self):
        for flags in ("-C elsewhere", "-n", "-deps", "-f={{.Name}}", "-unknown",
                      "-debug-actiongraph=graph.json", "bare", "-tags", "-run", "-test.tags=custom"):
            with self.subTest(flags=flags):
                result, commands = self.run_recipe(flags)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("ERROR:", result.stderr)
                self.assertFalse(any(name == "go" and args[0] in ("list", "test") for name, args, _, _ in commands))
                self.assertFalse(any(name == "meson" and args[0] == "test" for name, args, _, _ in commands))
        result, commands = self.run_recipe("-unknown", "./plain")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(name == "go" and args[0] == "list" for name, args, _, _ in commands))

    def test_automatic_selection_stops_at_every_binary_tail_boundary(self):
        for boundary in ("-args", "--args", "--"):
            with self.subTest(boundary=boundary):
                result, commands = self.run_recipe("-tags custom " + boundary + " -C elsewhere -race -unknown")
                self.assertEqual(result.returncode, 0, result.stderr)
                listing = next(args for name, args, _, _ in commands if name == "go" and args[0] == "list")
                self.assertEqual(listing, ["list", "-f", "{{.ImportPath}}", "-json=false", "-tags=custom", "./..."])
                invocation = next(args for name, args, _, _ in commands if name == "go" and args[0] == "test")
                self.assertLess(invocation.index("github.com/yanet-platform/yanet2/modules/l3b/controlplane"),
                                invocation.index(boundary))

    def test_real_go_binary_tail_keeps_default_and_explicit_selection(self):
        tail = "a'b;$HOME$(exit_99)*"
        for packages in ("", "./custom ./plain"):
            for boundary in ("-args", "--args"):
                with self.subTest(packages=packages, boundary=boundary):
                    result, commands = self.run_recipe("-tags=custom -v " + boundary + " -marker " + tail + " -tags=modulefile -race",
                                                       packages, real_go="all", fixture_files=self.context_files())
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("FIXTURE_SELECTED custom " + tail, result.stdout)
                    self.assertIn("FIXTURE_SELECTED plain " + tail, result.stdout)
                    invocation = next(args for name, args, _, _ in commands if name == "go" and args[0] == "test")
                    expected = "./custom" if packages else "recipe.invalid/context/custom"
                    self.assertLess(invocation.index(expected), invocation.index(boundary))
                    self.assertNotIn("recipe.invalid/context/modulefile", invocation)
                    self.assertNotIn("recipe.invalid/context/raceonly", invocation)

    def test_default_selection_and_order(self):
        result, commands = self.run_recipe()
        self.assertEqual(result.returncode, 0, result.stderr)
        operations = [(name, args[0]) for name, args, _, _ in commands if name in ("go", "meson")]
        self.assertEqual(operations, [("meson", "setup"), ("meson", "compile"), ("meson", "compile"), ("go", "list"), ("go", "test"), ("meson", "test")])
        invocation = next(command for command in commands if command[0] == "go" and command[1][0] == "test")
        self.assertEqual(invocation[1], ["test", "-count=1", "github.com/yanet-platform/yanet2/modules/acl/tests/functional", "github.com/yanet-platform/yanet2/modules/l3b/controlplane"])
        self.assertIn("-DTEST_INHERITED=1", invocation[2])
        self.assertIn("-DYANET_CACHE_LINE_SIZE=64", invocation[2])
        self.assertEqual(invocation[3], "-mod=mod")
        preflight = next(i for i, command in enumerate(commands) if command[0] == "python3")
        setup = next(i for i, command in enumerate(commands) if command[0] == "meson")
        self.assertLess(preflight, setup)
        self.assertTrue(any(name == "git" and args == ["config", "--global", "--add", "safe.directory", "/yanet2"] for name, args, _, _ in commands))

    def test_targeted_arguments_are_data(self):
        flags = "-race -count=20 -run ^Test_Backend_ -v"
        packages = "./modules/l3b/controlplane"
        result, commands = self.run_recipe(flags, packages)
        self.assertEqual(result.returncode, 0, result.stderr)
        invocation = next(command for command in commands if command[0] == "go" and command[1][0] == "test")
        self.assertEqual(invocation[1], ["test", "-count=1", packages, *flags.split()])
        self.assertFalse(any(name == "go" and args[0] == "list" for name, args, _, _ in commands))
        operations = [(name, args[0]) for name, args, _, _ in commands if name in ("go", "meson")]
        self.assertEqual(operations, [("meson", "setup"),
                                     ("meson", "compile"), ("meson", "compile"), ("go", "test"), ("meson", "test")])
        result, commands = self.run_recipe("-run a'b;$HOME$(error flags evaluated)* glob-*", packages + " $(error packages evaluated)\t./other")
        self.assertEqual(result.returncode, 0, result.stderr)
        invocation = next(command for command in commands if command[0] == "go" and command[1][0] == "test")
        self.assertEqual(invocation[1][2:], [packages, "$(error", "packages", "evaluated)", "./other", "-run", "a'b;$HOME$(error", "flags", "evaluated)*", "glob-*"])

    def test_independent_optional_arguments(self):
        for flags, packages, positional in (
            ("", "", []),
            ("-race", "", ["-race"]),
            ("", "./modules/l3b/controlplane", ["", "./modules/l3b/controlplane"]),
        ):
            with self.subTest(positional=positional):
                result, commands = self.run_recipe(positional=positional)
                self.assertEqual(result.returncode, 0, result.stderr)
                invocation = next(command for command in commands if command[0] == "go" and command[1][0] == "test")
                if flags:
                    self.assertEqual(invocation[1][-len(flags.split()):], flags.split())
                listed = any(name == "go" and args[0] == "list" for name, args, _, _ in commands)
                self.assertEqual(listed, not packages)
                self.assertNotIn("github.com/yanet-platform/yanet2/tests/functional/main", invocation[1])
                if packages:
                    self.assertEqual(invocation[1][-1], packages)

    def test_failures_propagate(self):
        operations = [("python3", "scripts/verify-docker-ccache.py"), ("python3", "scripts/test-meson-ccache.py"),
                      ("python3", "tests/recipes/dtest_test.py"), ("meson", "setup"),
                      ("python3", "scripts/check-meson-ccache.py"), ("meson", "compile"), ("meson", "compile"), ("go", "list"),
                      ("go", "test"), ("meson", "test")]
        for index, failure in ((0, "preflight"), (3, "setup"), (5, "compile"), (7, "list"), (8, "go"), (9, "meson")):
            with self.subTest(failure=failure):
                result, commands = self.run_recipe(fail=failure)
                self.assertNotEqual(result.returncode, 0)
                actual = [(name, args[0]) for name, args, _, _ in commands if name in ("python3", "go", "meson")]
                self.assertEqual(actual, operations[:index + 1])

    def test_empty_package_selection_stops_before_go_tests(self):
        for packages, listed in ((" \t\n", None), ("", ""),
                                 ("", "github.com/yanet-platform/yanet2/tests/functional/main")):
            with self.subTest(packages=packages, listed=listed):
                result, commands = self.run_recipe(packages=packages, listed_packages=listed)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(name == "go" and args[0] == "test" for name, args, _, _ in commands))
                self.assertFalse(any(name == "meson" and args[0] == "test" for name, args, _, _ in commands))

    def test_inherited_go_flags_remain_literal_or_undefined(self):
        for flags in (None, "-mod=mod $(error inherited flags evaluated)"):
            with self.subTest(flags=flags):
                result, commands = self.run_recipe(goflags=flags)
                self.assertEqual(result.returncode, 0, result.stderr)
                invocation = next(command for command in commands if command[0] == "go" and command[1][0] == "test")
                self.assertEqual(invocation[3], flags)

    def test_make_command_line_options_remain_literal_in_submakes(self):
        flags = "-run $(error test flags evaluated) glob-*"
        packages = "./modules/l3b/controlplane $(error packages evaluated)"
        inherited = "-mod=mod $(error inherited flags evaluated)"
        result, commands = self.run_recipe(recipe="_docker_run", positional=[
            "-it", 'make test "$@"', "sh", "GO_TEST_FLAGS=" + flags,
            "GO_TEST_PACKAGES=" + packages, "GOFLAGS=" + inherited,
        ])
        self.assertEqual(result.returncode, 0, result.stderr)
        invocation = next(command for command in commands if command[0] == "go" and command[1][0] == "test")
        self.assertEqual(invocation[1], ["test", "-count=1", *packages.split(), *flags.split()])
        self.assertEqual(invocation[3], inherited)

    def test_existing_build_helper_contract(self):
        result, commands = self.run_recipe(recipe="dbuild")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any(name == "meson" and args == ["compile", "-C", "build"] for name, args, _, _ in commands))

    def test_wrapper_preserves_command_and_positional_boundaries(self):
        arguments = ["space inside", "", "'quoted';$HOME$(error data)*"]
        result, commands = self.run_recipe(recipe="_docker_run", positional=[
            "-it", 'go test "$@"', "sh", *arguments,
        ])
        self.assertEqual(result.returncode, 0, result.stderr)
        invocation = next(args for name, args, _, _ in commands if name == "go")
        self.assertEqual(invocation, ["test", *arguments])

    def test_existing_docker_helpers_preserve_command_strings(self):
        for recipe, positional, command in (
            ("dsetup", ["true"], "just setup true"),
            ("dbuild", [], "just _ccache_setup && meson compile -C build"),
            ("dbuild-debug", [], "just build-debug"),
            ("dtidy", ["sample.c"], "just tidy sample.c"),
            ("dbloody", ["sample.c"], "just bloody sample.c"),
            ("dshell", [], "bash"),
            ("drun", ["go version"], "go version"),
            ("dcoverage", [], "just setup true && just coverage"),
            ("dcontrolplane", [], "make controlplane"),
            ("dcli", [], "make cli"),
            ("ddeb", [], "./scripts/build-deb.sh"),
            ("dfuzz", ["dscp"], "just fuzz dscp"),
        ):
            with self.subTest(recipe=recipe):
                result, commands = self.run_recipe(recipe=recipe, positional=positional, execute=False)
                self.assertEqual(result.returncode, 0, result.stderr)
                args = next(args for name, args, _, _ in commands if name == "docker")
                self.assertEqual(args[args.index("sh") + 5:], [command])

    def test_cache_mounts_survive_nested_recipes(self):
        for recipe in ("dbuild", "dtest"):
            for cache, override in ((None, None), ("/tmp/task cache'$HOME$(exit 99)", None),
                                    (None, "/tmp/override cache'$HOME$(exit 99)"),
                                    ("/tmp/environment cache", "/tmp/selected override")):
                with self.subTest(recipe=recipe, cache=cache, override=override):
                    result, commands = self.run_recipe(recipe=recipe, cache=cache, cache_override=override)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    args = next(args for name, args, _, _ in commands if name == "docker")
                    root = override or cache or "/tmp"
                    self.assertIn(root + "/gomodcache:/tmp/gomodcache:rw", args)
                    self.assertIn(root + "/gocache:/tmp/gocache:rw", args)
                    self.assertIn(root + "/ccache:/ccache:rw", args)


if __name__ == "__main__":
    print("RECIPE_RUNTIME " + json.dumps({"root": str(ROOT), "cwd": os.getcwd(), "euid": os.geteuid(),
                                         "go": subprocess.check_output([shutil.which("go"), "version"], text=True).strip(),
                                         "meson": subprocess.check_output([shutil.which("meson"), "--version"], text=True).strip(),
                                         "just": subprocess.check_output([shutil.which("just"), "--version"], text=True).strip()}), flush=True)
    program = unittest.main(verbosity=2, exit=False)
    for evidence in EVIDENCE:
        print("RECIPE_EVIDENCE " + json.dumps(evidence), flush=True)
    for evidence in ORDINARY_PREFLIGHT_EVIDENCE:
        print("ORDINARY_PREFLIGHT_EVIDENCE " + json.dumps(evidence), flush=True)
    for evidence in BOUNDARY_EVIDENCE:
        print("RECIPE_BOUNDARY_EVIDENCE " + json.dumps(evidence), flush=True)
    sys.exit(not program.result.wasSuccessful())
