#!/usr/bin/env python3

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

import go_cdeps


@unittest.skipUnless(shutil.which("go") and shutil.which("cc"), "Go and C compiler required")
class GoCdepsTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name).resolve()
        self.root = self.directory / "consumer/subprojects/router"
        self.build = self.directory / "build/subprojects/router"
        self.package = self.root / "bindings"
        self.package.mkdir(parents=True)
        self.build.mkdir(parents=True)
        (self.root / "go.mod").write_text("module example.org/router\n\ngo 1.20\n")
        (self.package / "binding.go").write_text(
            'package bindings\n/*\n#include "generated.h"\n*/\nimport "C"\n'
            'func Value() int { return int(C.VALUE) }\n'
        )
        self.header = self.build / "generated.h"
        self.header.write_text("#define VALUE 1\n")
        self.archive = self.build / "lib.a"
        self.archive.write_bytes(b"!<arch>\n")
        self.output = self.package / "zz_yanet_cdeps.c"
        self.stamp = self.build / "cdeps.stamp"

    def invoke(self, include=None, build_root=None):
        """Run the CLI against a minimal nested CGO package using real compiler discovery."""
        env = dict(os.environ, GOWORK="off", GOENV="off", GOFLAGS="",
                   GOTOOLCHAIN="local", CGO_ENABLED="1", CC="cc",
                   CGO_CPPFLAGS=f"-I{include or self.build}",
                   CGO_CFLAGS="", CGO_LDFLAGS="")
        return subprocess.run([
            sys.executable, str(Path(go_cdeps.__file__).resolve()),
            "--root", str(self.root), "--build-root", str(build_root or self.build),
            "--go", shutil.which("go"), "--package", "bindings",
            "--output", str(self.output), "--stamp", str(self.stamp), str(self.archive),
        ], env=env, text=True, capture_output=True)

    def test_nested_build_tracks_generated_header_changes(self):
        first = self.invoke()
        self.assertEqual(first.returncode, 0, first.stderr)
        marker = self.output.read_bytes()
        self.header.write_text("#define VALUE 2\n")
        changed = self.invoke()
        self.assertEqual(changed.returncode, 0, changed.stderr)
        self.assertNotEqual(marker, self.output.read_bytes())

    def test_header_escape_rejects_aliases_and_traversal_without_publishing(self):
        first = self.invoke()
        self.assertEqual(first.returncode, 0, first.stderr)
        before = self.output.read_bytes(), self.stamp.read_bytes()
        outside = self.directory / "outside"
        (outside / "child").mkdir(parents=True)
        external = outside / "generated.h"
        external.write_text("#define VALUE 2\n")
        alias = self.directory / "build-alias"
        alias.symlink_to(self.build, target_is_directory=True)
        self.header.unlink()
        self.header.symlink_to(external)
        link = self.build / "link"
        link.symlink_to(outside / "child", target_is_directory=True)
        for include, build_root in (
            (self.build, self.build),
            (alias, alias),
            (link / ".." / ".." / "outside", self.build),
        ):
            with self.subTest(include=include):
                self.output.write_bytes(before[0])
                self.stamp.write_bytes(before[1])
                failed = self.invoke(include, build_root)
                self.assertNotEqual(failed.returncode, 0, failed.stderr)
                self.assertEqual(
                    before, (self.output.read_bytes(), self.stamp.read_bytes()),
                )


if __name__ == "__main__":
    unittest.main()
