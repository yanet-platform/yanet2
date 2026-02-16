#!/bin/sh
# clang-format check entrypoint.
# Inputs (via environment variables set by GitHub Actions):
#   INPUT_INCLUDE        - newline-separated file name patterns (for find -name)
#   INPUT_EXCLUDE_REGEX  - extended regex to exclude file paths (grep -Ev)
#   INPUT_STYLE          - clang-format style (default: file)
set -e

STYLE="${INPUT_STYLE:-file}"
WORKSPACE="${GITHUB_WORKSPACE:-.}"

cd "$WORKSPACE" || { echo "ERROR: cannot cd to $WORKSPACE"; exit 1; }

# Collect files matching the include patterns.
FILELIST="$(mktemp)"
trap 'rm -f "$FILELIST"' EXIT

echo "$INPUT_INCLUDE" | while IFS= read -r pattern; do
    # Skip empty lines.
    [ -z "$pattern" ] && continue
    find . -type f -name "$pattern"
done | sort -u > "$FILELIST"

# Apply exclude regex if provided.
if [ -n "$INPUT_EXCLUDE_REGEX" ]; then
    FILTERED="$(mktemp)"
    grep -Ev "$INPUT_EXCLUDE_REGEX" "$FILELIST" > "$FILTERED" || true
    mv "$FILTERED" "$FILELIST"
fi

TOTAL="$(wc -l < "$FILELIST" | tr -d ' ')"
if [ "$TOTAL" -eq 0 ]; then
    echo "No files matched the include patterns."
    exit 0
fi

echo "Checking $TOTAL file(s) with clang-format (style=$STYLE)..."

FAILED=0
while IFS= read -r file; do
    if ! /usr/bin/clang-format --dry-run --Werror --style="$STYLE" "$file" 2>/dev/null; then
        FAILED=$((FAILED + 1))
        echo "--- Formatting diff for $file ---"
        /usr/bin/clang-format --style="$STYLE" "$file" | diff -u "$file" - || true
        echo ""
    fi
done < "$FILELIST"

if [ "$FAILED" -ne 0 ]; then
    echo "clang-format: $FAILED file(s) need formatting."
    exit 1
fi

echo "clang-format: all files are properly formatted."
