#!/bin/sh

# Parse and emit Go's effective GOFLAGS using its field grammar.

set -eu

if [ "$#" -ne 1 ]; then
	printf 'usage: %s <tag>\n' "$0" >&2
	exit 2
fi

exec python3 - "$1" <<'PY'
import os
import subprocess
import sys


def is_space(character):
    return character in " \t\n\r"


def split_go(value):
    fields = []
    position = 0
    while position < len(value):
        while position < len(value) and is_space(value[position]):
            position += 1
        if position == len(value):
            break

        if value[position] in "'\"":
            quote = value[position]
            end = value.find(quote, position + 1)
            if end == -1:
                raise ValueError(f"unterminated {quote} string")
            fields.append(value[position + 1:end])
            position = end + 1
            continue

        end = position
        while end < len(value) and not is_space(value[end]):
            end += 1
        fields.append(value[position:end])
        position = end
    return fields


def join_go(fields):
    rendered = []
    for field in fields:
        if not any(character in " \t\n\r'\"" for character in field):
            rendered.append(field)
        elif "'" not in field:
            rendered.append("'" + field + "'")
        elif '"' not in field:
            rendered.append('"' + field + '"')
        else:
            raise ValueError(
                f"cannot quote a field containing both quote types: {field!r}"
            )
    return " ".join(rendered)


def tags_from_value(value):
    if " " in value or "'" in value:
        return split_go(value)
    return [tag for tag in value.split(",") if tag]


def effective_go_flags():
    # Query Go so both an environment override and the persisted GOENV value
    # participate in the same parse.
    try:
        result = subprocess.run(
            ["go", "env", "GOFLAGS"],
            check=True,
            capture_output=True,
            text=True,
            env=os.environ.copy(),
        )
    except OSError as error:
        raise ValueError(f"cannot read effective GOFLAGS: {error}") from error
    except subprocess.CalledProcessError as error:
        details = error.stderr.strip()
        if details:
            raise ValueError(f"go env GOFLAGS failed: {details}") from error
        raise ValueError("go env GOFLAGS failed") from error
    return result.stdout.rstrip("\r\n")


def main():
    extra_tag = sys.argv[1]
    fields = split_go(effective_go_flags())
    flags = []
    effective_tags = []

    for field in fields:
        tag_value = None
        if field.startswith("-tags="):
            tag_value = field[len("-tags=") :]
        elif field.startswith("--tags="):
            tag_value = field[len("--tags=") :]
        elif field in ("-tags", "--tags"):
            raise ValueError(f"{field} must use =value in GOFLAGS")

        if tag_value is None:
            flags.append(field)
        else:
            # Go's flag parser replaces the value on every -tags occurrence.
            effective_tags = tags_from_value(tag_value)

    if extra_tag not in effective_tags:
        effective_tags.append(extra_tag)
    flags.append("-tags=" + ",".join(effective_tags))
    print(join_go(flags))


try:
    main()
except ValueError as error:
    print(f"go-flags.sh: {error}", file=sys.stderr)
    sys.exit(1)
PY
