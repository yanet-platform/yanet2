#!/bin/bash
set -euo pipefail

unset CDPATH
script_dir=${0%/*}
[[ "$script_dir" != "$0" ]] || script_dir=.
makefile=${1:-$script_dir/Makefile}
case "$makefile" in /*) ;; *) makefile="$PWD/$makefile";; esac
makefile_parent=$(cd -P "${makefile%/*}" && printf '%s.' "$PWD") || exit 1
makefile_parent=${makefile_parent%.}
makefile="$makefile_parent/${makefile##*/}"
make_bin=$(command -v make)
recipe_args=()
case ${2:-bash} in
    bash) recipe_args=("SHELL=$BASH");;
    default) ;;
    *) printf 'Unknown recipe mode: %s\n' "$2" >&2; exit 1;;
esac

sandbox=$(mktemp -d /tmp/shared-qemu-image.XXXXXX)
sandbox=$(cd "$sandbox" && pwd -P)
trap '/bin/rm -rf -- "$sandbox"' EXIT
mkdir "$sandbox/bin"
while IFS= read -r line || [[ -n "$line" ]]; do
    printf '%s\n' "${line//\/tmp\/yanet-test-vm/$sandbox\/vm}"
done < "$makefile" > "$sandbox/Makefile"

cat > "$sandbox/bin/tool" <<'STUB'
#!/bin/bash
set -euo pipefail
tool=${0##*/}
cd -P . || exit 90
[[ "$PWD" == "$SANDBOX" || "$PWD" == "$SANDBOX/"* ]] || exit 90
if [[ "$tool" == uname ]]; then printf '%s\n' "${FAKE_UNAME:-Linux}"; exit; fi
if [[ "$tool" == test ]]; then
    if [[ "${1-}" == "${FAIL_COMPARE-}" && "${2-}" == -ef ]]; then exit 2; fi
    exec /bin/test "$@"
fi
printf '%s\n' "$tool" >> "$EFFECTS"
safe_path() {
    local path=$1 parent name
    [[ "$path" == /* ]] || path="$PWD/$path"
    name=${path##*/}
    [[ -n "$name" && "$name" != . && "$name" != .. ]] || exit 91
    [[ ! -L "$path" ]] || exit 91
    parent=${path%/*}
    [[ -d "$parent" ]] || exit 91
    parent=$(cd -P "$parent" && printf '%s.' "$PWD") || exit 91
    parent=${parent%.}
    [[ "$parent" == "$SANDBOX" || "$parent" == "$SANDBOX/"* ]] || exit 91
}
write_iso() {
    local path=$1 parent name
    [[ "$path" == /* ]] || path="$PWD/$path"
    parent=${path%/*}
    name=${path##*/}
    [[ -n "$name" && "$name" != . && "$name" != .. && -d "$parent" ]] || exit 91
    parent=$(cd -P "$parent" && printf '%s.' "$PWD") || exit 91
    parent=${parent%.}
    [[ "$parent" == "$SANDBOX" || "$parent" == "$SANDBOX/"* ]] || exit 91
    path="$parent/$name"
    [[ ! -e "$path" && ! -L "$path" ]] || exit 91
    printf 'iso\n' > "$path"
}
case "$tool" in
    which) exit 0;;
    find)
        [[ "$1" == "$SANDBOX/vm" && "${FAIL_FIND-}" != 1 ]] || exit 94
        exec /usr/bin/find "$@";;
    rm|mkdir|cp)
        for path in "$@"; do
            [[ "$path" == -* ]] && continue
            safe_path "$path"
        done
        exec "/bin/$tool" "$@";;
    wget)
        [[ $# == 2 && "$1" == -c ]] || exit 93
        name=${2##*/}
        [[ -n "$name" && "$name" != . && "$name" != .. ]] || exit 91
        printf '%q\n' "$@" > "$SANDBOX/wget-args"
        printf 'ubuntu\n' > "$name";;
    mkisofs)
        [[ "${1-}" == -output && $# -ge 2 ]] || exit 93
        printf '%q\n' "$@" > "$SANDBOX/iso-args"
        write_iso "$2";;
    hdiutil)
        [[ $# == 8 && "$1" == makehybrid && "$2" == -iso && "$3" == -joliet &&
            "$4" == -default-volume-name && "$5" == cidata && "$6" == -o ]] || exit 93
        printf '%q\n' "$@" > "$SANDBOX/iso-args"
        write_iso "$7";;
    go) printf '%q\n' "${YANET_QEMU_IMAGE-}" "${YANET_TEST_DEBUG-}" "$@" "$PWD" > "$SANDBOX/go-call";;
    qemu-img) printf '%q\n' "$@" > "$SANDBOX/qemu-img-args";;
    qemu-system-x86_64)
        for path in "$@"; do
            case "$path" in
                -fsdev\ local,*) path=${path#*path=}; path=${path%%,*}; [[ -d "$path" ]] || exit 96;;
            esac
        done
        printf '%q\n' "$@" > "$SANDBOX/qemu-args";;
    *) exit 92;;
esac
STUB
chmod +x "$sandbox/bin/tool"
for tool in uname which test find rm mkdir cp wget mkisofs hdiutil qemu-img qemu-system-x86_64 go; do
    ln -s tool "$sandbox/bin/$tool"
done

ubuntu=ubuntu-24.04-minimal-cloudimg-amd64.img
artifacts=(yanet-test.qcow2 yanet-test-booted-v3.qcow2 yanet-test-baseline-test.qcow2
    cloud-init-autologin.iso qemu_debug.log qemu.log test_output-one.log test_run.log)
sidecars=(yanet-test-baseline-test.qcow2.lock yanet-test-baseline-test.qcow2.sha256)
case_number=0

new_case() {
    case_number=$((case_number + 1))
    fixture="$sandbox/case-$case_number"
    work="$fixture/work"
    shared="$fixture/shared image"
    mkdir -p "$work/build" "$shared"
    image="$shared/base.qcow2"
    printf 'shared image\n' > "$image"
    printf 'shared neighbor\n' > "$shared/neighbor.qcow2"
    printf 'shared image\n' > "$fixture/shared-image.expected"
    printf 'shared neighbor\n' > "$fixture/shared-neighbor.expected"
    for path in "${artifacts[@]}" "$ubuntu" "${sidecars[@]}"; do printf 'shared neighbor\n' > "$shared/$path"; done
    effects="$fixture/effects"
    : > "$effects"
}

run_make() {
    local selected=$1
    shift
    local env_args=()
    [[ "$selected" == '<unset>' ]] || env_args=("YANET_QEMU_IMAGE=$selected")
    : > "$effects"
    env -i PATH="$sandbox/bin:/usr/bin:/bin" HOME="$sandbox" LC_ALL=C \
        SANDBOX="$sandbox" EFFECTS="$effects" FAKE_UNAME="${fake_uname:-Linux}" FAIL_FIND="${fail_find-}" \
        FAIL_COMPARE="${fail_compare-}" YANET_TEST_DEBUG=fixture \
        ${env_args[@]+"${env_args[@]}"} "$make_bin" --no-print-directory -j4 \
        -C "$work" -f "$sandbox/Makefile" KVM_AVAILABLE=no \
        ${recipe_args[@]+"${recipe_args[@]}"} "$@"
}

expect_go() {
    local selected=$1
    shift
    { printf '%q\n' "$selected" fixture; printf '%q\n' "$@" "$work"; } > "$fixture/expected-go-call"
    cmp -s "$fixture/expected-go-call" "$sandbox/go-call"
}

expect_shared_go() {
    local path
    expect_go "$@"
    [[ "$(< "$effects")" == go ]]
    for path in "${artifacts[@]}" "$ubuntu" cloud-init-tmp; do [[ ! -e "$work/$path" ]]; done
}

has_qemu_arg() {
    local expected
    printf -v expected '%q' "$1"
    /usr/bin/grep -Fqx -- "$expected" "$sandbox/qemu-args"
}

has_qemu_arg_prefix() {
    local expected arg
    printf -v expected '%q' "$1"
    while IFS= read -r arg; do [[ "$arg" == "$expected"* ]] && return 0; done < "$sandbox/qemu-args"
    return 1
}

expect_qemu() {
    local mode=$1 drive arg expected_drives=1
    local -a expected
    case "$mode" in
        prepare-vm|debug-vm) drive=file=yanet-test.qcow2,if=virtio,format=qcow2,discard=unmap,detect-zeroes=unmap;;
        debug-vm-ssh) drive=file=yanet-test.qcow2,if=virtio,format=qcow2;;
        *) return 1;;
    esac
    expected=(
        -drive "$drive"
        -netdev stream,id=net1,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_0.sock
        -netdev stream,id=net2,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_1.sock
        -fsdev "local,id=fsdev0,path=$sandbox/vm,security_model=none"
        -device virtio-9p-pci,fsdev=fsdev0,mount_tag=logs
        -fsdev "local,id=fsdev1,path=$sandbox/vm,security_model=none"
        -device virtio-9p-pci,fsdev=fsdev1,mount_tag=config
        -fsdev "local,id=fsdev2,path=$work/build,security_model=none"
        -device virtio-9p-pci,fsdev=fsdev2,mount_tag=build
        -fsdev "local,id=fsdev3,path=$work/target,security_model=none"
        -device virtio-9p-pci,fsdev=fsdev3,mount_tag=target
        -fsdev "local,id=fsdev4,path=$work,security_model=none"
        -device virtio-9p-pci,fsdev=fsdev4,mount_tag=yanet2
    )
    if [[ "$mode" == prepare-vm ]]; then
        expected_drives=2
        expected+=(-drive file=cloud-init-autologin.iso,if=virtio,format=raw)
    else
        ! has_qemu_arg 'file=cloud-init-autologin.iso,if=virtio,format=raw' || return 1
    fi
    for arg in "${expected[@]}"; do has_qemu_arg "$arg" || return 1; done
    [[ $(/usr/bin/grep -cx -- -drive "$sandbox/qemu-args") == "$expected_drives" ]] || return 1
    has_qemu_arg_prefix "virtio-net-pci,bus=pcie.1,netdev=net1," || return 1
    has_qemu_arg_prefix "virtio-net-pci,bus=pcie.2,netdev=net2," || return 1
    if [[ "$mode" == debug-vm-ssh ]]; then
        /usr/bin/grep -Fq 'hostfwd=tcp:127.0.0.1:2222-:22' "$sandbox/qemu-args"
    else
        ! /usr/bin/grep -Fq 'hostfwd=' "$sandbox/qemu-args"
    fi
}

seed_local() {
    local path
    for path in "${artifacts[@]}" "$ubuntu"; do printf 'local\n' > "$work/$path"; done
    mkdir -p "$sandbox/vm"
    printf 'vm\n' > "$sandbox/vm/log"
}

shared_intact() {
    cmp -s "$fixture/shared-image.expected" "$image"
    cmp -s "$fixture/shared-neighbor.expected" "$shared/neighbor.qcow2"
    local path
    for path in "${artifacts[@]}" "$ubuntu" "${sidecars[@]}"; do cmp -s "$fixture/shared-neighbor.expected" "$shared/$path"; done
}

no_effects() { [[ ! -s "$effects" ]]; }

# Invalid shared images and debug targets must fail without side effects.
new_case
run_make "$image" prepare-vm
no_effects
run_make "$image" check-image
no_effects
printf 'relative input\n' > "$work/relative"
printf 'unreadable\n' > "$shared/unreadable"
chmod 000 "$shared/unreadable"
[[ ! -r "$shared/unreadable" ]] || { printf 'Run the sandbox as an unprivileged user\n' >&2; exit 1; }
for invalid in relative "$shared/missing" "$shared" "$shared/unreadable" '   '; do
    for target in prepare-vm check-image all test test-main test-run clean clean-all; do
        if run_make "$invalid" "$target"; then exit 1; fi
        no_effects
    done
done
for target in debug-vm debug-vm-ssh; do
    if run_make "$image" "$target"; then exit 1; fi
    no_effects
done
shared_intact

# A fake ISO writer must reject paths outside its fixture, including symlink escapes.
new_case
ln -s "$fixture" "$work/outside"
iso_escape_parent="$work"$'\n'
/bin/mkdir "$iso_escape_parent"
for output in "$fixture/escaped.iso" "$work/outside/escaped-via-link.iso" "$iso_escape_parent/escaped-with-newline.iso"; do
    if (cd "$work" && env SANDBOX="$work" EFFECTS="$effects" FAKE_UNAME=Linux "$sandbox/bin/mkisofs" -output "$output"); then exit 1; fi
    [[ ! -e "$output" ]]
done

# Fake filesystem tools reject paths that leave a fixture through a symlink.
new_case
ln -s "$fixture" "$work/outside"
printf 'preserve\n' > "$fixture/victim"
printf 'copy source\n' > "$work/source"
escape_parent="$fixture"$'\n'
/bin/mkdir "$escape_parent"
printf 'newline preserve\n' > "$escape_parent/victim"
if (cd "$work" && env SANDBOX="$work" EFFECTS="$effects" "$sandbox/bin/rm" -f "$work/outside/victim"); then exit 1; fi
if (cd "$work" && env SANDBOX="$work" EFFECTS="$effects" "$sandbox/bin/mkdir" "$work/outside/newdir"); then exit 1; fi
if (cd "$work" && env SANDBOX="$work" EFFECTS="$effects" "$sandbox/bin/cp" "$work/source" "$work/outside/victim"); then exit 1; fi
if (cd "$work" && env SANDBOX="$work" EFFECTS="$effects" "$sandbox/bin/cp" "$work/source" "$work/.."); then exit 1; fi
if (cd "$work" && env SANDBOX="$fixture" EFFECTS="$effects" "$sandbox/bin/rm" -f "$escape_parent/victim"); then exit 1; fi
for path in "$work/.." "$work/"; do
    if (cd "$work" && env SANDBOX="$work" EFFECTS="$effects" "$sandbox/bin/rm" -rf "$path"); then exit 1; fi
done
[[ "$(< "$fixture/victim")" == preserve && "$(< "$escape_parent/victim")" == 'newline preserve' && ! -e "$fixture/newdir" ]]

# Fake tools reject a symlinked working directory outside their fixture.
new_case
ln -s "$fixture" "$work/outside-cwd"
if (cd "$work/outside-cwd" && env SANDBOX="$work" EFFECTS="$effects" "$sandbox/bin/wget" -c https://example.invalid/base.img); then exit 1; fi
[[ ! -e "$fixture/base.img" ]]

# Image names are passed literally to subprocesses, and shared test targets stay read-only.
new_case
image="$shared/\$(go injected) ' ;.qcow2"
printf 'shared image\n' > "$image"
run_make "$image" test-main
expect_shared_go "$image" test -count=1 -v -failfast ./main/...
run_make '<unset>' "YANET_QEMU_IMAGE=$image" test-main
expect_shared_go "$image" test -count=1 -v -failfast ./main/...
run_make "$image" -e test-main
expect_shared_go "$image" test -count=1 -v -failfast ./main/...
run_make "$image" test
expect_shared_go "$image" test -count=1 -v ./main/...
run_make "$image" test-run 'TEST=TestOne|TestTwo'
expect_shared_go "$image" test -count=1 -v -run 'TestOne|TestTwo' ./...
run_make "$image" all
expect_shared_go "$image" test -count=1 -v ./main/...
run_make "$image"
expect_shared_go "$image" test -count=1 -v ./main/...
shared_intact

# Make must not evaluate shell expressions in the shared image path at parse time.
new_case
image="$shared/\$(shell go injected) ' ;.qcow2"
printf 'shared image\n' > "$image"
for origin in environment environment-with-e command-line; do
    case "$origin" in
        environment) run_make "$image" -n help;;
        environment-with-e) run_make "$image" -e -n help;;
        command-line) run_make '<unset>' "YANET_QEMU_IMAGE=$image" -n help;;
    esac
    no_effects
    shared_intact
done

# Shared cleanup preserves the image, neighbours and aliases.
for target in clean clean-all; do
    new_case
    seed_local
    printf 'unrelated user snapshot\n' > "$work/user-snapshot.qcow2"
    run_make "$image" "$target"
    expect_go "$image" clean -testcache
    shared_intact
    [[ "$(< "$work/user-snapshot.qcow2")" == 'unrelated user snapshot' ]]
    for path in "${artifacts[@]}"; do [[ ! -e "$work/$path" ]]; done
    [[ ! -e "$sandbox/vm" ]]
    if [[ "$target" == clean ]]; then [[ -f "$work/$ubuntu" ]]; else [[ ! -e "$work/$ubuntu" ]]; fi

    new_case
    seed_local
    /bin/rm "$work/yanet-test.qcow2" "$work/cloud-init-autologin.iso" "$work/qemu.log"
    ln "$image" "$work/yanet-test.qcow2"
    ln -s "$image" "$work/cloud-init-autologin.iso"
    ln -s "$shared/neighbor.qcow2" "$work/qemu.log"
    run_make "$image" "$target"
    expect_go "$image" clean -testcache
    [[ "$work/yanet-test.qcow2" -ef "$image" && -L "$work/cloud-init-autologin.iso" && -L "$work/qemu.log" ]]
    [[ ! -e "$work/test_run.log" ]]
    shared_intact
done

# Cleanup preserves a selected-image hardlink at the VM root.
new_case
seed_local
/bin/rm -rf "$sandbox/vm"
ln "$image" "$sandbox/vm"
run_make "$image" clean
expect_go "$image" clean -testcache
[[ "$sandbox/vm" -ef "$image" && ! -e "$work/test_run.log" ]]
shared_intact
/bin/rm "$sandbox/vm"

# Cleanup preserves a VM-root symlink into shared storage.
new_case
seed_local
/bin/rm -rf "$sandbox/vm"
ln -s "$shared" "$sandbox/vm"
run_make "$image" clean
expect_go "$image" clean -testcache
[[ -L "$sandbox/vm" && -f "$work/$ubuntu" && ! -e "$work/test_run.log" ]]
shared_intact
/bin/rm "$sandbox/vm"

for kind in hardlink symlink; do
    new_case
    seed_local
    mkdir "$sandbox/vm/nested"
    if [[ "$kind" == hardlink ]]; then ln "$image" "$sandbox/vm/nested/alias"; else ln -s "$image" "$sandbox/vm/nested/alias"; fi
    run_make "$image" clean-all
    expect_go "$image" clean -testcache
    [[ -e "$sandbox/vm/nested/alias" && ! -e "$work/test_run.log" ]]
    shared_intact
    /bin/rm -rf "$sandbox/vm"
done

for variable in QEMU_IMAGE CLOUD_INIT_AUTOLOGIN_ISO UBUNTU_IMAGE; do
    new_case
    seed_local
    if run_make "$image" clean-all "$variable=$shared/neighbor.qcow2"; then exit 1; fi
    no_effects
    [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]]
    shared_intact
    /bin/rm -rf "$sandbox/vm"
done

new_case
seed_local
if run_make "$work/yanet-test.qcow2" clean-all; then exit 1; fi
no_effects
[[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]]
/bin/rm -rf "$sandbox/vm"

# Reject overlap even when the image reaches the VM directory through an alias.
for overlap in inside-vm vm-inside-shared vm-symlink vm-parent-alias; do
    new_case
    seed_local
    case "$overlap" in
        inside-vm) printf 'shared image\n' > "$sandbox/vm/base.qcow2"; selected="$sandbox/vm/base.qcow2";;
        vm-inside-shared) printf 'shared image\n' > "$sandbox/base.qcow2"; selected="$sandbox/base.qcow2";;
        vm-symlink) ln -s "$shared" "$sandbox/vm/shared"; selected="$sandbox/vm/shared/base.qcow2";;
        vm-parent-alias) printf 'shared image\n' > "$sandbox/vm/base.qcow2"; ln -s "$sandbox/vm" "$fixture/vm-link"; selected="$fixture/vm-link/base.qcow2";;
    esac
    if run_make "$selected" clean-all; then exit 1; fi
    no_effects
    [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]]
    shared_intact
    /bin/rm -rf "$sandbox/vm"
done

# A newline-terminated shared directory still protects direct neighbours.
new_case
seed_local
newline_shared="$shared"$'\n\n'
/bin/mv "$shared" "$newline_shared"
shared=$newline_shared
image="$shared/base.qcow2"
if run_make "$image" clean-all "QEMU_IMAGE=$shared/neighbor.qcow2"; then exit 1; fi
no_effects
[[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]]
shared_intact
/bin/rm -rf "$sandbox/vm"

for kind in absolute relative; do
    new_case
    seed_local
    mkdir "$fixture/links" "$fixture/hops"
    hop="$fixture/hops/hop"$'\n\n'
    selected="$fixture/links/base"
    if [[ "$kind" == absolute ]]; then
        ln -s "$image" "$hop"
        ln -s "$hop" "$selected"
    else
        ln -s '../shared image/base.qcow2' "$hop"
        ln -s "../hops/hop"$'\n\n' "$selected"
    fi
    printf 'neighbor\n' > "$fixture/hops/neighbor"
    for neighbor in "$fixture/hops/neighbor" "$shared/neighbor.qcow2"; do
        if run_make "$selected" clean-all "QEMU_IMAGE=$neighbor"; then exit 1; fi
        no_effects
        [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]]
        shared_intact
    done
    /bin/rm -rf "$sandbox/vm"
done

# Cleanup preserves a valid symlink-selected image and its worktree aliases.
for kind in absolute relative; do
    new_case
    seed_local
    mkdir "$fixture/image-links"
    selected="$fixture/image-links/base"
    if [[ "$kind" == absolute ]]; then ln -s "$image" "$selected"; else ln -s '../shared image/base.qcow2' "$selected"; fi
    /bin/rm "$work/yanet-test.qcow2" "$work/cloud-init-autologin.iso"
    ln -s "$selected" "$work/yanet-test.qcow2"
    ln "$image" "$work/cloud-init-autologin.iso"
    run_make "$selected" clean-all
    expect_go "$selected" clean -testcache
    [[ -L "$selected" && "$selected" -ef "$image" ]]
    [[ -L "$work/yanet-test.qcow2" && "$work/cloud-init-autologin.iso" -ef "$image" ]]
    [[ ! -e "$work/test_run.log" && ! -e "$sandbox/vm" ]]
    shared_intact
done

for variable in QEMU_IMAGE CLOUD_INIT_AUTOLOGIN_ISO UBUNTU_IMAGE; do
    new_case
    seed_local
    mkdir "$fixture/local"$'\n\n'
    path="$fixture/local"$'\n\n''/artifact $cash.qcow2'
    printf 'independent\n' > "$path"
    assignment="$variable=\$(CURDIR)/../local"$'\n\n''/artifact $$cash.qcow2'
    run_make "$image" clean-all "$assignment"
    expect_go "$image" clean -testcache
    [[ ! -e "$path" && ! -e "$work/test_run.log" ]]
    shared_intact
done

for fault in find compare; do
    for target in clean clean-all; do
        new_case
        seed_local
        ln "$image" "$sandbox/vm/alias"
        fail_find= fail_compare=
        if [[ "$fault" == find ]]; then fail_find=1; else fail_compare="$sandbox/vm/alias"; fi
        if run_make "$image" "$target"; then exit 1; fi
        [[ "$(< "$effects")" == find ]]
        for path in "${artifacts[@]}" "$ubuntu"; do [[ "$(< "$work/$path")" == local ]]; done
        [[ "$(< "$sandbox/vm/log")" == vm && "$sandbox/vm/alias" -ef "$image" ]]
        shared_intact
        unset fail_find fail_compare
        /bin/rm -rf "$sandbox/vm"
    done
done

# Both goal orders preflight every operand before making any changes.
for order in 'clean clean-all' 'clean-all clean'; do
    new_case
    seed_local
    if run_make "$image" $order "UBUNTU_IMAGE=$shared/neighbor.qcow2"; then exit 1; fi
    no_effects
    [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]]
    shared_intact
    run_make "$image" $order
    expect_go "$image" clean -testcache
    [[ ! -e "$work/test_run.log" && ! -e "$work/$ubuntu" && ! -e "$sandbox/vm" ]]
    printf '%s\n' find rm rm rm rm rm rm rm rm rm rm go > "$fixture/expected-effects"
    cmp -s "$fixture/expected-effects" "$effects"
    shared_intact
done

# The default local-image path retains its Linux and macOS media, Go and cleanup behavior.
for selected in '<unset>' ''; do
    for os in Linux Darwin; do
        new_case
        fake_uname=$os
        printf 'user data\n' > "$work/cloud-init-user-data.yaml"
        printf 'metadata\n' > "$work/meta-data"
        run_make "$selected" prepare-vm
        [[ -f "$work/yanet-test.qcow2" && -f "$work/cloud-init-autologin.iso" ]]
        printf '%q\n' -c https://cloud-images.ubuntu.com/minimal/releases/noble/release/ubuntu-24.04-minimal-cloudimg-amd64.img > "$fixture/expected-wget-args"
        cmp -s "$fixture/expected-wget-args" "$sandbox/wget-args"
        printf '%q\n' resize yanet-test.qcow2 10G > "$fixture/expected-qemu-img-args"
        cmp -s "$fixture/expected-qemu-img-args" "$sandbox/qemu-img-args"
        expect_qemu prepare-vm
        case "$os" in
            Linux) expected_iso=(-output cloud-init-autologin.iso -volid cidata -joliet -rock cloud-init-tmp/user-data cloud-init-tmp/meta-data);;
            Darwin) expected_iso=(makehybrid -iso -joliet -default-volume-name cidata -o cloud-init-autologin.iso cloud-init-tmp);;
        esac
        printf '%q\n' "${expected_iso[@]}" > "$fixture/expected-iso-args"
        cmp -s "$fixture/expected-iso-args" "$sandbox/iso-args"

        for target in test test-main test-run all default; do
            case "$target" in
                test) run_make "$selected" "$target"; expect_go '' test -count=1 -v ./main/...;;
                test-main) run_make "$selected" "$target"; expect_go '' test -count=1 -v -failfast ./main/...;;
                test-run) run_make "$selected" "$target" 'TEST=TestOne|TestTwo'; expect_go '' test -count=1 -v -run 'TestOne|TestTwo' ./...;;
                all) run_make "$selected" "$target"; expect_go '' test -count=1 -v ./main/...;;
                default) run_make "$selected"; expect_go '' test -count=1 -v ./main/...;;
            esac
            [[ $(/usr/bin/grep -c '^go$' "$effects") == 1 ]]
            effect_log=$(< "$effects")
            [[ "$effect_log" != *wget* && "$effect_log" != *mkisofs* && "$effect_log" != *hdiutil* && "$effect_log" != *qemu-img* ]]
        done

        for target in debug-vm debug-vm-ssh; do
            run_make "$selected" "$target"
            expect_qemu "$target"
        done

        seed_local
        printf 'unrelated user snapshot\n' > "$work/user-snapshot.qcow2"
        run_make "$selected" clean
        expect_go '' clean -testcache
        for path in "${artifacts[@]}"; do [[ ! -e "$work/$path" ]]; done
        [[ -f "$work/$ubuntu" && ! -e "$sandbox/vm" ]]
        [[ "$(< "$work/user-snapshot.qcow2")" == 'unrelated user snapshot' ]]
        [[ $(/usr/bin/grep -c '^go$' "$effects") == 1 ]]
        shared_intact

        seed_local
        run_make "$selected" clean-all
        expect_go '' clean -testcache
        for path in "${artifacts[@]}" "$ubuntu"; do [[ ! -e "$work/$path" ]]; done
        [[ ! -e "$sandbox/vm" ]]
        [[ "$(< "$work/user-snapshot.qcow2")" == 'unrelated user snapshot' ]]
        [[ $(/usr/bin/grep -c '^go$' "$effects") == 1 ]]
        shared_intact
        unset fake_uname
    done
done

# A separate clean ignores an unsafe Ubuntu override; clean-all refuses it afterward.
new_case
seed_local
if ! run_make "$image" clean "UBUNTU_IMAGE=$shared/neighbor.qcow2"; then exit 1; fi
expect_go "$image" clean -testcache
for path in "${artifacts[@]}"; do [[ ! -e "$work/$path" ]]; done
[[ -f "$work/$ubuntu" && ! -e "$sandbox/vm" ]]
[[ $(/usr/bin/grep -c '^go$' "$effects") == 1 ]]
shared_intact
if run_make "$image" clean-all "UBUNTU_IMAGE=$shared/neighbor.qcow2"; then exit 1; fi
no_effects
[[ -f "$work/$ubuntu" ]]
shared_intact

printf 'PASS: shared/local Makefile sandbox (%s fixtures)\n' "$case_number"
