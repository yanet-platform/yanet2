#!/bin/bash
set -Eeuo pipefail

unset CDPATH
script_dir=$(cd -- "$(dirname -- "$0")" && pwd -P)
make_bin=$(command -v make)
recipe_args=()
case ${1:-bash} in
    bash) recipe_args=("SHELL=$BASH");;
    default) ;;
    *) printf 'Usage: %s [bash|default]\n' "$0" >&2; exit 1;;
esac
[[ $(id -u) -ne 0 ]] || { echo 'Run as an unprivileged user' >&2; exit 1; }
sandbox=$(mktemp -d)
trap 'rm -rf -- "$sandbox"' EXIT
scenario=setup
trap 'printf "FAIL: %s (line %s)\n" "$scenario" "$LINENO" >&2; cat "$sandbox/output" >&2' ERR
mkdir "$sandbox/bin" "$sandbox/work" "$sandbox/shared image"
work="$sandbox/work"
image="$sandbox/shared image/base.qcow2"
effects="$sandbox/effects"
printf 'shared\n' > "$image"
artifacts=(test_run.log yanet-test.qcow2 cloud-init-autologin.iso ubuntu-24.04-minimal-cloudimg-amd64.img)
for artifact in "${artifacts[@]}"; do printf 'local\n' > "$work/$artifact"; done
cp "$script_dir/Makefile" "$sandbox/Makefile"

cat > "$sandbox/bin/tool" <<'STUB'
#!/bin/bash
set -eu
tool=${0##*/}
case "$tool" in
    uname) echo Linux; exit 0;;
    which)
        # Local bootstrap dependencies are unavailable in shared fixtures.
        if [[ -z "${YANET_QEMU_IMAGE-}" ]]; then exit 0; fi;;
esac
printf '%s\n' "$tool" >> "$EFFECTS"
if [[ "$tool" == go ]]; then
    printf '%s\0' "${YANET_QEMU_IMAGE-}" "$@" > "$GO_CALL"
    exit 0
fi
exit 97
STUB
chmod +x "$sandbox/bin/tool"
for tool in uname which go rm cp mkdir wget mkisofs hdiutil qemu-img qemu-system-x86_64 find readlink; do
    ln -s tool "$sandbox/bin/$tool"
done

# Runs the copied Makefile with recording tools that never mutate fixtures.
run_make() {
    local selected=$1
    shift
    local env_args=()
    [[ "$selected" == '<unset>' ]] || env_args=("YANET_QEMU_IMAGE=$selected")
    : > "$effects"
    env -i PATH="$sandbox/bin:/usr/bin:/bin" HOME="$sandbox" LC_ALL=C \
        EFFECTS="$effects" GO_CALL="$sandbox/go-call" \
        ${env_args[@]+"${env_args[@]}"} "$make_bin" --no-print-directory -j4 \
        -C "$work" -f "$sandbox/Makefile" KVM_AVAILABLE=no \
        ${recipe_args[@]+"${recipe_args[@]}"} "$@" > "$sandbox/output" 2>&1
}

# Compares the recorded environment and argument boundaries, not rendered output.
expect_go() {
    printf '%s\0' "$@" > "$sandbox/expected"
    cmp "$sandbox/expected" "$sandbox/go-call"
    [[ $(< "$effects") == go ]]
}

scenario='shared preparation preserves existing local artifacts'
run_make "$image" prepare-vm
[[ ! -s "$effects" && $(< "$image") == shared ]]
for artifact in "${artifacts[@]}"; do [[ $(< "$work/$artifact") == local ]]; done

scenario='invalid shared inputs fail before tools run'
printf 'unreadable\n' > "$sandbox/unreadable"
chmod 000 "$sandbox/unreadable"
for invalid in relative "$sandbox/missing" "$sandbox/unreadable" "$sandbox/shared image"; do
    for target in prepare-vm test test-main test-run; do
        scenario="invalid shared input: $invalid / $target"
        if run_make "$invalid" "$target" TEST=TestOne; then false; fi
        [[ ! -s "$effects" ]]
    done
done

scenario='shared test targets preserve literal paths and Go arguments'
image="$sandbox/shared image/\$(go injected) ' ;.qcow2"
printf 'shared\n' > "$image"
for target in test test-main test-run all default; do
    scenario="shared Go arguments: $target"
    case "$target" in
        test-main) run_make "$image" "$target"; expect_go "$image" test -count=1 -v -failfast ./main/...;;
        test-run) run_make "$image" "$target" 'TEST=TestOne|TestTwo'; expect_go "$image" test -count=1 -v -run 'TestOne|TestTwo' ./...;;
        default) run_make "$image"; expect_go "$image" test -count=1 -v ./main/...;;
        *) run_make "$image" "$target"; expect_go "$image" test -count=1 -v ./main/...;;
    esac
done
for origin in environment environment-with-e command-line; do
    scenario="literal Make expression: $origin"
    selected="$sandbox/shared image/\$(shell go injected).qcow2"
    case "$origin" in
        environment) run_make "$selected" -n help;;
        environment-with-e) run_make "$selected" -e -n help;;
        command-line) run_make '<unset>' "YANET_QEMU_IMAGE=$selected" -n help;;
    esac
    [[ ! -s "$effects" ]]
    case "$origin" in
        environment) run_make "$image" test-main;;
        environment-with-e) run_make "$image" -e test-main;;
        command-line) run_make '<unset>' "YANET_QEMU_IMAGE=$image" test-main;;
    esac
    expect_go "$image" test -count=1 -v -failfast ./main/...
done

for selected in "$image" "$sandbox/missing"; do
    for goals in clean clean-all 'clean clean-all' 'clean-all clean'; do
        scenario="shared cleanup refuses: $goals / $selected"
        if run_make "$selected" $goals "QEMU_IMAGE=$image" "UBUNTU_IMAGE=$image"; then
            echo "FAIL: $scenario unexpectedly succeeded" >&2; exit 1
        fi
        [[ ! -s "$effects" && $(< "$image") == shared ]]
        for artifact in "${artifacts[@]}"; do [[ $(< "$work/$artifact") == local ]]; done
    done
done
for target in debug-vm debug-vm-ssh; do
    scenario="shared debug refuses: $target"
    if run_make "$image" "$target"; then false; fi
    [[ ! -s "$effects" ]]
done

for selected in '<unset>' ''; do
    scenario="local mode with ready image: $selected"
    touch "$work/cloud-init-user-data.yaml" "$work/meta-data"
    touch "$work/ubuntu-24.04-minimal-cloudimg-amd64.img" "$work/cloud-init-autologin.iso"
    touch "$work/yanet-test.qcow2"
    run_make "$selected" prepare-vm
    [[ ! -s "$effects" ]]
    run_make "$selected" test-main
    expect_go '' test -count=1 -v -failfast ./main/...
    for target in clean clean-all; do
        scenario="local cleanup reaches blocked removal: $selected / $target"
        if run_make "$selected" "$target"; then false; fi
        [[ $(< "$effects") == rm && $(< "$work/test_run.log") == local ]]
    done
    scenario="cold local preparation reaches blocked bootstrap: $selected"
    rm "$work/yanet-test.qcow2" "$work/ubuntu-24.04-minimal-cloudimg-amd64.img"
    if run_make "$selected" prepare-vm; then false; fi
    [[ $(< "$effects") == wget ]]
done
echo 'PASS: shared/local Makefile contracts'
