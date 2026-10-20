#!/usr/bin/env python3

import argparse
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile


def go_words(value):
    """Preserve the Go toolchain's quoting and literal backslashes."""
    words = []
    while value:
        value = value.lstrip(" \t\n\r")
        if not value:
            break
        if value[0] in "\"'":
            end = value.find(value[0], 1)
            if end < 0:
                raise ValueError("unterminated quoted Go compiler flag")
            words.append(value[1:end])
            value = value[end + 1:]
        else:
            end = next(
                (index for index, char in enumerate(value) if char in " \t\n\r"),
                len(value),
            )
            words.append(value[:end])
            value = value[end:]
    return words


def run(command, directory, env=None):
    result = subprocess.run(
        command, cwd=directory, env=env, text=True,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    if result.returncode:
        detail = result.stderr.strip() or result.stdout.strip()
        raise RuntimeError(f"{command[0]} exited with {result.returncode}: {detail}")
    return result.stdout


def dependency_paths(value):
    """Decode compiler dependency paths without treating them as shell words."""
    value = value.replace("\\\r\n", " ").replace("\\\n", " ")
    for line in value.split("\n"):
        _, separator, dependencies = line.partition(":")
        if not separator:
            if line.strip(" \t"):
                raise ValueError("compiler emitted an invalid dependency rule")
            continue
        word = []
        index = 0
        while index < len(dependencies):
            char = dependencies[index]
            if char in " \t":
                if word:
                    yield "".join(word)
                    word = []
                index += 1
            elif char == "#":
                break
            elif char == "\\":
                end = index
                while end < len(dependencies) and dependencies[end] == "\\":
                    end += 1
                count = end - index
                following = dependencies[end:end + 1]
                if following and following in " \t":
                    word.append("\\" * (count // 2))
                    if count % 2:
                        word.append(following)
                        end += 1
                elif following == "#":
                    word.append("\\" * (count - 1) + "#")
                    end += 1
                else:
                    word.append("\\" * count)
                index = end
            elif dependencies[index:index + 2] == "$$":
                word.append("$")
                index += 2
            else:
                word.append(char)
                index += 1
        if word:
            yield "".join(word)


def discover(root, package, go, output, stamp):
    """Use active CGO sources and compiler includes as the dependency authority."""
    directory = (root / package).resolve(strict=True)
    directory.relative_to(root)
    metadata = json.loads(run([go, "list", "-find", "-json", "."], directory))
    if not metadata.get("CgoFiles"):
        raise ValueError("no active CGO sources")
    for kind in (
        "CXXFiles", "MFiles", "FFiles", "SFiles", "SwigFiles", "SwigCXXFiles",
        "CgoPkgConfig",
    ):
        if metadata.get(kind):
            raise ValueError(f"dependency discovery does not support {kind}")
    config = json.loads(run([
        go, "env", "-json", "CC", "GOGCCFLAGS", "CGO_CPPFLAGS", "CGO_CFLAGS",
        "GOFLAGS", "GOTMPDIR",
    ], directory))
    sanitizers = {}
    for flag in go_words(config["GOFLAGS"]):
        name, separator, value = flag.partition("=")
        if name.lstrip("-") in ("overlay", "toolexec") and value:
            raise ValueError(f"dependency discovery does not support -{name.lstrip('-')}")
        if name.lstrip("-") in ("asan", "msan"):
            sanitizers[name.lstrip("-")] = (
                not separator or value.lower() in ("1", "t", "true")
            )
    sanitizer_flags = [
        "-fsanitize=" + sanitizer
        for option, sanitizer in (("asan", "address"), ("msan", "memory"))
        if sanitizers.get(option)
    ]
    go_temporary = config["GOTMPDIR"] or os.environ.get("TMPDIR") or "/tmp"
    if (
        any(char in go_temporary for char in " \t\n\r")
        and "'" in go_temporary and '"' in go_temporary
    ):
        raise ValueError(
            "Go temporary paths with whitespace and both quote characters are unsupported"
        )
    compiler = go_words(config["CC"])
    compiler_flags = go_words(config["GOGCCFLAGS"])
    # Extra compiler command words can remain in Go's reported implicit flags.
    prefix = (compiler + ["-I", "."])[3:]
    if prefix and compiler_flags[:len(prefix)] == prefix:
        compiler_flags = compiler_flags[len(prefix):]
    with tempfile.TemporaryDirectory(prefix="yanet-go-cdeps-") as temporary:
        scratch = Path(temporary).resolve()
        flags = (
            go_words(config["CGO_CPPFLAGS"]) + metadata.get("CgoCPPFLAGS", [])
            + ["-I", str(scratch)]
            + sanitizer_flags
            + go_words(config["CGO_CFLAGS"]) + metadata.get("CgoCFLAGS", [])
        )
        compiler_env = dict(os.environ, CC=config["CC"])
        run([
            go, "tool", "cgo", "-objdir", str(scratch) + os.sep,
            "-importpath", metadata["ImportPath"], "--",
        ] + flags + [str(directory / name) for name in metadata["CgoFiles"]],
            directory, compiler_env)
        sources = [scratch / "_cgo_export.c"] + sorted(scratch.glob("*.cgo2.c")) + [
            directory / name for name in metadata.get("CFiles", [])
            if not (
                name == "zz_yanet_cdeps.c"
                and (directory / name).resolve() == output
            )
        ]
        dependencies = run(
            compiler + ["-I", str(directory)] + compiler_flags + flags
            + ["-M", "-MT", "go-cdeps", "-MF", "-"]
            + [str(path) for path in sources],
            directory,
        )
        if not dependencies.strip():
            raise ValueError("compiler did not emit dependency rules")
        paths = set()
        for name in dependency_paths(dependencies):
            lexical = Path(os.path.abspath(directory / name))
            path = (directory / name).resolve(strict=True)
            if path in (output, stamp):
                raise ValueError("marker and stamp must not replace a native input")
            try:
                path.relative_to(root)
            except ValueError:
                try:
                    lexical.relative_to(root)
                except ValueError:
                    continue
                raise ValueError(f"project dependency resolves outside the root: {lexical}")
            if scratch in path.parents:
                continue
            paths.add(path)
        return paths


def fingerprint(root, inputs):
    """Hash native inputs independently of checkout location and mtimes."""
    digest = hashlib.sha256()

    def frame(value):
        digest.update(struct.pack(">Q", len(value)))
        digest.update(value)

    frame(b"yanet-go-cdeps-v1")
    paths = []
    for value in inputs:
        path = value.resolve(strict=True)
        paths.append((path.relative_to(root).as_posix(), path))
    for logical_path, path in sorted(paths):
        frame(logical_path.encode("utf-8"))
        frame(path.read_bytes())
    return digest.hexdigest()


def stage_changed(path, content):
    """Stage changed bytes beside their destination for atomic replacement."""
    try:
        if path.read_bytes() == content:
            return None
    except FileNotFoundError:
        pass

    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    temporary = Path(temporary)
    try:
        with os.fdopen(descriptor, "wb") as output:
            os.fchmod(output.fileno(), 0o644)
            output.write(content)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise
    return temporary


def publish(root, inputs, output, stamp):
    """Read every input before staging the marker, then publish the stamp last."""
    value = fingerprint(root, inputs)
    marker = (
        "static const char yanet_go_cdeps_fingerprint_v1[] "
        f'__attribute__((used)) = "{value}";\n'
    ).encode("ascii")
    staged = []
    try:
        for path, content in ((output, marker), (stamp, f"{value}\n".encode("ascii"))):
            temporary = stage_changed(path, content)
            if temporary is not None:
                staged.append((temporary, path))
        for temporary, path in staged:
            os.replace(temporary, path)
    finally:
        for temporary, _ in staged:
            temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(
        description="Publish a CGO source marker for compiler-discovered native inputs."
    )
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--go", required=True)
    parser.add_argument("--package", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--stamp", required=True, type=Path)
    parser.add_argument("inputs", nargs="*", type=Path)
    args = parser.parse_args()
    try:
        root = args.root.resolve(strict=True)
        inputs = [path.resolve(strict=True) for path in args.inputs]
        if args.output.is_symlink() or args.stamp.is_symlink():
            raise ValueError("marker and stamp must not be symlinks")
        output = args.output.resolve()
        stamp = args.stamp.resolve()
        if output == stamp:
            raise ValueError("marker and stamp must have different paths")
        if output in inputs or stamp in inputs:
            raise ValueError("marker and stamp must not replace a native input")
        if args.package.is_absolute():
            raise ValueError("package must be a path relative to the project root")
        inputs = set(inputs) | discover(
            root, args.package, args.go, output, stamp,
        )
        publish(root, inputs, output, stamp)
    except (OSError, RuntimeError, ValueError) as error:
        print(f"go_cdeps: {args.package}: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
