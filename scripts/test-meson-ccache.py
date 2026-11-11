#!/usr/bin/env python3
"""Exercise the Docker setup guard with disposable real Meson projects."""

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


root = Path(__file__).resolve().parent.parent
with tempfile.TemporaryDirectory(prefix="meson-ccache-") as temp:
    for name, cc, cxx, cached, preexisting in (
        ("discovered", None, None, True, False),
        ("explicit-clang", "ccache clang", "ccache clang++", True, False),
        ("absolute-wrapper", "/usr/bin/ccache cc", "/usr/bin/ccache c++", True, False),
        ("masquerade", None, None, True, False),
        ("path-masquerade", "cc", "c++", True, False),
        ("missing-executable", None, None, False, True),
        ("real-compiler-symlink", None, None, False, True),
        ("stale-cc", "cc", "c++", False, True),
        ("uncached-clang", "clang", "clang++", False, True),
        ("uncached-cxx", "ccache cc", "c++", False, True),
        ("fresh-uncached", "cc", "c++", False, False),
    ):
        source = Path(temp) / name
        source.mkdir()
        shutil.copy(root / "Justfile", source / "Justfile")
        (source / "scripts").mkdir()
        shutil.copy(root / "scripts/check-meson-ccache.py", source / "scripts")
        (source / "meson.build").write_text(
            "project('cache-fixture', 'c', 'cpp')\n"
            "executable('fixture', 'main.c', 'other.cpp')\n"
        )
        (source / "main.c").write_text("int main(void) { return 0; }\n")
        (source / "other.cpp").write_text("int other() { return 42; }\n")
        env = os.environ.copy()
        env.pop("CC", None)
        env.pop("CXX", None)
        if cc is not None:
            env.update(CC=cc, CXX=cxx)
        if name in ("masquerade", "path-masquerade", "missing-executable", "real-compiler-symlink"):
            masquerade = source / "masquerade"
            masquerade.mkdir()
            for link in ("cc", "c++"):
                target = link if name == "real-compiler-symlink" else "ccache"
                target_path = shutil.which(target)
                if target_path is None:
                    sys.exit(f"fixture requires {target} in PATH")
                os.symlink(target_path, masquerade / link)
            if name == "path-masquerade":
                env["PATH"] = f"{masquerade}{os.pathsep}{env['PATH']}"
            else:
                env.update(CC=str(masquerade / "cc"), CXX=str(masquerade / "c++"))
        if preexisting:
            subprocess.run(["meson", "setup", "build"], cwd=source, env=env, check=True)
            (source / "build/sentinel").write_text("user-owned artifact\n")
        if name == "missing-executable":
            (masquerade / "cc").unlink()
        before = {
            path.relative_to(source): (path.read_bytes(), path.stat().st_mtime_ns)
            for path in (source / "build").rglob("*") if path.is_file()
        }
        result = subprocess.run(
            ["just", "_ccache_setup"], cwd=source, env=env,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        print(result.stdout, end="")
        if cached:
            assert result.returncode == 0, name
            subprocess.run(
                ["meson", "compile", "-C", "build"], cwd=source, env=env, check=True
            )
            if name == "explicit-clang":
                assert "ccache clang" in (source / "build/build.ninja").read_text()
        else:
            assert result.returncode != 0, name
            assert "Manually reconfigure or recreate" in result.stdout, name
            if preexisting:
                after = {
                    path.relative_to(source): (path.read_bytes(), path.stat().st_mtime_ns)
                    for path in (source / "build").rglob("*") if path.is_file()
                }
                assert before == after, f"{name}: existing build changed"
        print(f"Meson cache regression OK: {name}", flush=True)
