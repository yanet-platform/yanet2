#!/usr/bin/env python3

import argparse
import hashlib
import os
from pathlib import Path
import struct
import sys
import tempfile


def fingerprint(root, inputs):
    """Hash explicit native inputs independently of checkout location and mtimes."""
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
        description="Publish a CGO source marker for explicit native inputs."
    )
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--stamp", required=True, type=Path)
    parser.add_argument("inputs", nargs="+", type=Path)
    args = parser.parse_args()
    try:
        root = args.root.resolve(strict=True)
        inputs = [path.resolve(strict=True) for path in args.inputs]
        output = args.output.resolve()
        stamp = args.stamp.resolve()
        if output == stamp:
            raise ValueError("marker and stamp must have different paths")
        if output in inputs or stamp in inputs:
            raise ValueError("marker and stamp must not replace a native input")
        publish(root, inputs, output, stamp)
    except (OSError, RuntimeError, ValueError) as error:
        print(f"go_cdeps: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
