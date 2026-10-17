#!/bin/bash
set -euo pipefail
printf 'Sandbox Bash: %s\n' "$BASH_VERSION"
recipe_args=()
case ${2:-bash} in
    bash) recipe_args=("SHELL=$BASH"); printf 'Sandbox recipe shell: %s\n' "$BASH";;
    default) printf 'Sandbox recipe shell: default /bin/sh (no override)\n';;
    *) printf 'Unknown recipe mode: %s\n' "$2" >&2; exit 1;;
esac

unset CDPATH
makefile=${1:-${0%/*}/Makefile}
[[ "$0" == */* || -n "${1-}" ]] || makefile=Makefile
case "$makefile" in /*) ;; *) makefile="./$makefile";; esac
parent=$(cd -P "${makefile%/*}/." && pwd -P && printf .)
parent=${parent%.}
parent=${parent%?}
makefile="$parent/${makefile##*/}"
[[ -f "$makefile" ]] || { printf 'Missing Makefile: %s\n' "$makefile" >&2; exit 1; }
make_bin=$(command -v make)
"$make_bin" --version
# Legacy recipes need a whitespace-free fixture root, regardless of caller temp settings.
sandbox=$(mktemp -d /tmp/shared-qemu-image.XXXXXX)
sandbox=$(cd "$sandbox" && pwd -P)
trap '/bin/rm -rf -- "$sandbox"' EXIT
mkdir "$sandbox/bin"

# The static VM directory is redirected before any recipe can execute.
while IFS= read -r line || [[ -n "$line" ]]; do
    printf '%s\n' "${line//\/tmp\/yanet-test-vm/$sandbox\/vm}"
done < "$makefile" > "$sandbox/Makefile"

cat > "$sandbox/bin/tool" <<'STUB'
#!/bin/bash
set -euo pipefail
tool=${0##*/}
[[ "$PWD/" == "$SANDBOX/"* ]] || exit 90
if [[ "$tool" == uname ]]; then
    printf '%s\n' "$FAKE_OS"
    exit
fi
record=$(
    printf '%s image=%q debug=%q' "$tool" "${YANET_QEMU_IMAGE-<unset>}" "$YANET_TEST_DEBUG"
    printf ' %q' "$@"
)
if [[ "$tool" == test ]]; then
    if [[ "$#" == 3 && "${1-}" == "${FAKE_COMPARE_ERROR_PATH-}" && "${2-}" == -ef && "${3-}" == "${YANET_QEMU_IMAGE-}" ]]; then
        printf '%s status=2\n' "$record" >> "$COMPARISON_ERRORS"
        exit 2
    fi
    exec /usr/bin/test "$@"
fi
printf '%s\n' "$record" >> "$EFFECTS"

# Only disposable paths may reach a mutating host utility.
check_path() {
    local path=$1
    [[ "$path" == /* ]] || path="$PWD/$path"
    [[ "$path" == "$SANDBOX/"* && "$path" != *'/../'* && "$path" != */.. ]] || exit 91
}
case "$tool" in
    which)
        [[ "$FAKE_MISSING" != all && "$FAKE_MISSING" != "$1" ]]
        ;;
    rm|mkdir)
        for path in "$@"; do
            [[ "$path" == -* ]] || check_path "$path"
        done
        "/bin/$tool" "$@"
        ;;
    cp)
        for path in "$@"; do check_path "$path"; done
        /bin/cp "$@"
        ;;
    find)
        [[ "$1" == "$SANDBOX/vm" ]] || exit 93
        [[ "$FAKE_MISSING" != find ]] || exit 94
        /usr/bin/find "$@"
        ;;
    wget)
        [[ "$#" == 2 && "$1" == -c && "$2" == https://cloud-images.ubuntu.com/* ]]
        printf 'download fixture\n' > "${2##*/}"
        ;;
    hdiutil)
        while [[ "$1" != -o ]]; do shift; done
        check_path "$2"
        printf 'ISO fixture\n' > "$2"
        ;;
    mkisofs)
        [[ "$1" == -output ]]
        check_path "$2"
        printf 'ISO fixture\n' > "$2"
        ;;
    qemu-img)
        [[ "$#" == 3 && "$1" == resize && "$3" == 10G ]]
        check_path "$2"
        ;;
    go|qemu-system-x86_64) ;;
    *) exit 92;;
esac
STUB
chmod +x "$sandbox/bin/tool"
for tool in uname which go qemu-img qemu-system-x86_64 wget curl mkisofs hdiutil rm cp mkdir find test; do
    ln -s tool "$sandbox/bin/$tool"
done

artifacts=(yanet-test.qcow2 yanet-test-booted-v3.qcow2 yanet-test-baseline-test.qcow2
    cloud-init-autologin.iso qemu_debug.log qemu.log test_output-one.log test_run.log)
ubuntu=ubuntu-24.04-minimal-cloudimg-amd64.img
case_number=0
os=Darwin
missing=all
printf 'shared base\000\377\n\n' > "$sandbox/base.expected"
printf 'shared neighbor\000\377\n\n' > "$sandbox/neighbor.expected"

# Each case owns its image, neighbors, working directory and recorded effects.
new_case() {
    case_name=$1
    case_number=$((case_number + 1))
    fixture="$sandbox/case-$case_number"
    work="$fixture/work"
    shared="$fixture/shared image"
    mkdir -p "$work" "$shared"
    touch "$work/all" "$work/clean" "$work/clean-all"
    printf 'cloud-init fixture\n' > "$work/cloud-init-user-data.yaml"
    printf 'metadata fixture\n' > "$work/meta-data"
    image="$shared/yanet-test.qcow2"
    /bin/cp "$sandbox/base.expected" "$image"
    for path in "${artifacts[@]:1}" "$ubuntu" sidecar; do
        /bin/cp "$sandbox/neighbor.expected" "$shared/$path"
    done
    effects="$fixture/effects"
    : > "$effects"
    comparison_errors="$fixture/comparison-errors"
    : > "$comparison_errors"
}

# Failures include the exact subprocess records, not just Make's messages.
fail() {
    printf 'FAIL: %s: %s\n' "$case_name" "$*" >&2
    while IFS= read -r line; do printf 'effect: %s\n' "$line" >&2; done < "$effects"
    printf '%s\n' "${make_output-}" >&2
    exit 1
}

# No caller Make settings or real image export enter a fixture invocation.
run_make() {
    local selected=$1
    shift
    local env_args=()
    [[ "$selected" == '<unset>' ]] || env_args+=("YANET_QEMU_IMAGE=$selected")
    : > "$effects"
    : > "$comparison_errors"
    make_output=$(env -i PATH="$sandbox/bin:/usr/bin:/bin" HOME="$sandbox" LC_ALL=C \
        SANDBOX="$sandbox" EFFECTS="$effects" FAKE_OS="$os" FAKE_MISSING="$missing" \
        COMPARISON_ERRORS="$comparison_errors" FAKE_COMPARE_ERROR_PATH="${comparison_error_path-}" \
        YANET_TEST_DEBUG=fixture-debug CDPATH="${fixture_cdpath-}" ${env_args[@]+"${env_args[@]}"} \
        "$make_bin" --no-print-directory -j4 -C "$work" -f "$sandbox/Makefile" KVM_AVAILABLE=no ${recipe_args[@]+"${recipe_args[@]}"} "$@" 2>&1)
}

# Validation and rejection may read metadata but must launch no effectful tools.
expect_no_effects() {
    [[ ! -s "$effects" ]] || fail 'unexpected subprocess'
}

# The serialization matches the stub, preserving argv boundaries and exact env.
expect_go() {
    local selected=$1
    shift
    local expected actual
    expected=$(printf 'go image=%q debug=%q' "$selected" fixture-debug; printf ' %q' "$@")
    actual=$(< "$effects")
    [[ "$actual" == "$expected" ]] || fail 'Go argv or environment changed'
    printf 'argv/env: %s\n' "$actual"
}

# Local tests retain every dependency probe in order before the unchanged Go argv.
expect_local_go() {
    local selected=$1 tool iso=mkisofs
    shift
    [[ "$os" != Darwin ]] || iso=hdiutil
    {
        for tool in "$iso" qemu-img qemu-system-x86_64 go; do
            printf 'which image=%q debug=%q %q\n' "$selected" fixture-debug "$tool"
        done
        printf 'go image=%q debug=%q' "$selected" fixture-debug
        printf ' %q' "$@"
        printf '\n'
    } > "$fixture/expected-effects"
    cmp -s "$fixture/expected-effects" "$effects" || fail 'full local dependency/Go sequence changed'
    while IFS= read -r line; do printf 'local argv/env: %s\n' "$line"; done < "$effects"
}

# Expected records retain the same argument boundaries as the fake subprocesses.
write_effect() {
    local tool=$1 selected=$2
    shift 2
    printf '%s image=%q debug=%q' "$tool" "$selected" fixture-debug
    printf ' %q' "$@"
    printf '\n'
}

# The recursive safety probe retains every shell-program and pathname argument.
write_scan_effect() {
    local selected=$1
    write_effect find "$selected" "$sandbox/vm" -exec sh -c 'image=$1; shift; for path in "$@"; do if [ -L "$path" ]; then printf "%s\n" "$path" || exit "$?"; elif env test "$path" -ef "$image"; then printf "%s\n" "$path" || exit "$?"; else status=$?; [ "$status" -eq 1 ] || exit "$status"; fi; done; exit 0' sh "$selected" '{}' +
}

# Local bootstrap and both debug modes pin the complete legacy QEMU argv.
write_qemu_effect() {
    local selected=$1 mode=$2
    local args=()
    if [[ "$mode" == ssh ]]; then
        args+=(-netdev user,id=n1,hostfwd=tcp:127.0.0.1:2222-:22
            -device virtio-net-pci,netdev=n1,mac=AA:BB:CC:DD:CA:B0)
    fi
    args+=(-name yanet-test-debug -machine q35,kernel-irqchip=split -cpu max -smp 4 -m 1G
        -device intel-iommu,intremap=on,device-iotlb=on
        -device ioh3420,id=pcie.1,chassis=1 -device ioh3420,id=pcie.2,chassis=2)
    if [[ "$mode" == ssh ]]; then
        args+=(-drive file=yanet-test.qcow2,if=virtio,format=qcow2)
    else
        args+=(-drive file=yanet-test.qcow2,if=virtio,format=qcow2,discard=unmap,detect-zeroes=unmap)
    fi
    if [[ "$mode" == prepare ]]; then
        args+=(-drive file=cloud-init-autologin.iso,if=virtio,format=raw)
    fi
    if [[ "$mode" != ssh ]]; then
        args+=(-netdev user,id=net0 -device virtio-net-pci,netdev=net0,mac=AA:BB:CC:DD:CA:B0)
    fi
    args+=(-netdev stream,id=net1,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_0.sock
        -device virtio-net-pci,bus=pcie.1,netdev=net1,mac=52:54:00:6b:ff:a5,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10
        -netdev stream,id=net2,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_1.sock
        -device virtio-net-pci,bus=pcie.2,netdev=net2,mac=52:54:00:11:00:03,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10
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
        -serial mon:stdio -no-reboot -display none)
    write_effect qemu-system-x86_64 "$selected" "${args[@]}"
}

# Parallel preparation may interleave probes and bootstrap, but no records may be lost.
expect_local_prepare() {
    local selected=$1 tool iso=mkisofs
    [[ "$os" != Darwin ]] || iso=hdiutil
    {
        for tool in "$iso" qemu-img qemu-system-x86_64 go; do write_effect which "$selected" "$tool"; done
        write_effect wget "$selected" -c "https://cloud-images.ubuntu.com/minimal/releases/noble/release/$ubuntu"
        write_effect rm "$selected" -f cloud-init-autologin.iso
        write_effect mkdir "$selected" -p cloud-init-tmp
        write_effect cp "$selected" cloud-init-user-data.yaml cloud-init-tmp/user-data
        write_effect cp "$selected" meta-data cloud-init-tmp/meta-data
        if [[ "$os" == Darwin ]]; then
            write_effect hdiutil "$selected" makehybrid -iso -joliet -default-volume-name cidata -o cloud-init-autologin.iso cloud-init-tmp
        else
            write_effect mkisofs "$selected" -output cloud-init-autologin.iso -volid cidata -joliet -rock cloud-init-tmp/user-data cloud-init-tmp/meta-data
        fi
        write_effect rm "$selected" -rf cloud-init-tmp
        write_effect cp "$selected" "$ubuntu" yanet-test.qcow2
        write_effect qemu-img "$selected" resize yanet-test.qcow2 10G
        write_effect mkdir "$selected" -p "$work/target" "$sandbox/vm"
        write_qemu_effect "$selected" prepare
    } | LC_ALL=C sort > "$fixture/expected-effects"
    LC_ALL=C sort "$effects" > "$fixture/sorted-effects"
    cmp -s "$fixture/expected-effects" "$fixture/sorted-effects" || fail 'full local preparation effect log changed'
    while IFS= read -r line; do printf 'local argv/env: %s\n' "$line"; done < "$effects"
}

# Existing local media leaves only directory creation and the selected debug launch.
expect_local_debug() {
    local selected=$1 mode=$2
    {
        write_effect mkdir "$selected" -p "$work/target" "$sandbox/vm"
        write_qemu_effect "$selected" "$mode"
    } > "$fixture/expected-effects"
    cmp -s "$fixture/expected-effects" "$effects" || fail 'full local debug effect log changed'
    while IFS= read -r line; do printf 'local argv/env: %s\n' "$line"; done < "$effects"
}

# Local cleanup retains its exact removal order and one cache-clear command.
expect_local_cleanup() {
    local selected=$1 target=$2
    local booted=yanet-test-booted-v3.qcow2 baseline=yanet-test-baseline-test.qcow2 output=test_output-one.log
    if [[ "$target" == clean-all ]]; then
        booted='yanet-test-booted*.qcow2'; baseline='yanet-test-baseline*.qcow2'; output='test_output*.log'
    fi
    {
        write_effect rm "$selected" -f yanet-test.qcow2
        write_effect rm "$selected" -f "$booted" "$baseline"
        write_effect rm "$selected" -f cloud-init-autologin.iso
        write_effect rm "$selected" -f qemu_debug.log qemu.log "$output" test_run.log
        write_effect rm "$selected" -rf "$sandbox/vm"
        write_effect go "$selected" clean -testcache
        if [[ "$target" == clean-all ]]; then write_effect rm "$selected" -f "$ubuntu"; fi
    } > "$fixture/expected-effects"
    cmp -s "$fixture/expected-effects" "$effects" || fail 'full local cleanup effect log changed'
    while IFS= read -r line; do printf 'local argv/env: %s\n' "$line"; done < "$effects"
}

# Cleanup may only probe the local VM tree, remove fixture paths and clear Go's cache.
expect_cleanup() {
    local selected=$1 line go_calls=0 expected
    expected=$(printf 'go image=%q debug=%q clean -testcache' "$selected" fixture-debug)
    while IFS= read -r line; do
        case "$line" in
            rm\ *) printf 'destructive argv/env: %s\n' "$line";;
            find\ *) ;;
            go\ *)
                [[ "$line" == "$expected" ]] || fail 'cleanup Go argv or environment changed'
                go_calls=$((go_calls + 1))
                ;;
            *) fail 'unexpected cleanup subprocess';;
        esac
    done < "$effects"
    [[ "$go_calls" == 1 ]] || fail 'cleanup omitted or repeated Go cache clearing'
}

# External base and adjacent snapshots remain byte-for-byte fixture data.
expect_shared_intact() {
    cmp -s "$sandbox/base.expected" "$image" || fail 'shared input bytes changed'
    for path in "${artifacts[@]:1}" "$ubuntu" sidecar; do
        cmp -s "$sandbox/neighbor.expected" "$shared/$path" || fail "shared neighbor bytes changed: $path"
    done
}

# Independent local artifacts remain eligible even when shared aliases exist.
seed_local() {
    for path in "${artifacts[@]}" "$ubuntu"; do printf 'local artifact\n' > "$work/$path"; done
    mkdir -p "$sandbox/vm"
    printf 'local VM artifact\n' > "$sandbox/vm/log"
}

# Separate fixtures report every review regression before the matrix gate stops.
run_review_case() {
    new_case "$1"
    shift
    if (trap - EXIT; "$@"); then
        printf 'PASS: %s\n' "$case_name"
    else
        review_failures=$((review_failures + 1))
    fi
    /bin/rm -rf -- "$sandbox/vm"
}

# Verifies that a selected file or inode alias at the recursive cleanup root
# remains protected.
test_vm_root_file() {
    local kind=$1
    seed_local
    /bin/rm -rf -- "$sandbox/vm"
    if [[ "$kind" == input ]]; then
        /bin/cp "$image" "$sandbox/vm"
        image="$sandbox/vm"
    else
        ln "$image" "$sandbox/vm"
    fi
    run_make "$image" clean-all || fail 'root-file protection rejected independent cleanup'
    expect_cleanup "$image"
    [[ "$sandbox/vm" -ef "$image" ]] || fail 'VM-root input or inode alias removed'
    [[ ! -e "$work/test_run.log" && ! -e "$work/$ubuntu" ]] || fail 'independent cleanup suppressed'
    expect_shared_intact
    /bin/rm -- "$sandbox/vm"
}

# Verifies that artifact overrides are checked as whole paths before any
# removal occurs.
test_shared_operand() {
    local variable=$1 spelling=$2 path
    shift 2
    if (( $# == 0 )); then set -- clean-all; fi
    seed_local
    ln -s "$shared" "$fixture/shared-alias"
    [[ "$fixture/shared-alias" -ef "$shared" ]] || fail 'shared directory metadata alias fixture is invalid'
    case "$variable" in
        QEMU_IMAGE) path=yanet-test-booted-v3.qcow2;;
        CLOUD_INIT_AUTOLOGIN_ISO) path=cloud-init-autologin.iso;;
        UBUNTU_IMAGE) path=$ubuntu;;
    esac
    if [[ "$spelling" == spaces ]]; then path="$shared/$path"; else path="$fixture/shared-alias/$path"; fi
    if run_make "$image" "$@" "$variable=$path"; then fail 'shared-neighbor operand accepted'; fi
    expect_no_effects
    [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]] || fail 'operand rejected after removal'
    expect_shared_intact
    /bin/rm -rf -- "$sandbox/vm"
}

# Verifies that cleanup removes a safe absolute override with spaces as one
# operand, without splitting words.
test_independent_operand() {
    local variable=$1 spelling=${2-ordinary} path parent expected line removed=0
    seed_local
    parent="$fixture/local media"
    [[ "$spelling" != newline ]] || parent+=$'\n\n'
    mkdir "$parent" || fail 'fixture directory creation failed'
    path="$parent/independent artifact"
    printf 'local bytes\n' > "$path" || fail 'fixture write failed'
    run_make "$image" clean-all "$variable=$path" || fail 'independent override cleanup rejected'
    expect_cleanup "$image"
    expected=$(write_effect rm "$image" -f -- "$path")
    while IFS= read -r line; do
        if [[ "$line" == "$expected" ]]; then removed=$((removed + 1)); fi
    done < "$effects"
    [[ "$removed" == 1 && ! -e "$path" ]] || fail 'independent operand was split or not removed exactly once'
    expect_shared_intact
}

# Verifies that command-line and overriding environment image paths stay literal
# while trusted artifact configuration expands during startup.
test_literal_origin() {
    local origin=$1 initializer=${2-} line config_calls=0 test_calls=0 expected_config expected_config_unset expected_test
    local config_args=()
    image="$shared/\$(shell go make-expanded) \$(go literal) ' \" ;.qcow2"
    /bin/cp "$sandbox/base.expected" "$image"
    if [[ -n "$initializer" ]]; then
        config_args=("$initializer=\$(shell go startup-config)trusted-artifact")
    fi
    if [[ "$origin" == command-line ]]; then
        run_make '<unset>' test-main "YANET_QEMU_IMAGE=$image" ${config_args[@]+"${config_args[@]}"} || fail 'command-line literal path rejected'
    else
        run_make "$image" -e test-main ${config_args[@]+"${config_args[@]}"} || fail 'environment-with-e literal path rejected'
    fi
    if [[ -z "$initializer" ]]; then
        expect_go "$image" test -count=1 -v -failfast ./main/...
    else
        expected_config=$(write_effect go "$image" startup-config)
        expected_config_unset=$expected_config
        if [[ "$origin" == command-line ]]; then
            # Older Make leaves command-line exports out of parse-time subprocesses.
            expected_config_unset=$(write_effect go '<unset>' startup-config)
        fi
        expected_test=$(write_effect go "$image" test -count=1 -v -failfast ./main/...)
        while IFS= read -r line; do
            if [[ "$line" == "$expected_config" || "$line" == "$expected_config_unset" ]]; then
                config_calls=$((config_calls + 1))
            elif [[ "$line" == "$expected_test" ]]; then
                test_calls=$((test_calls + 1))
            else
                fail 'startup evaluated filename data or changed argv/environment'
            fi
        done < "$effects"
        [[ "$config_calls" -gt 0 && "$test_calls" == 1 ]] || fail 'trusted configuration expansion or exact test launch missing'
        printf 'startup config calls=%s; test argv/env: %s\n' "$config_calls" "$expected_test"
    fi
    expect_shared_intact
}

# Verifies that combined goals remove each local artifact and clear the cache
# exactly once.
test_combined_cleanup() {
    local line probes=0 removals=0 path
    seed_local
    run_make "$image" "$@" || fail 'combined cleanup failed'
    expect_cleanup "$image"
    while IFS= read -r line; do
        case "$line" in
            find\ *) probes=$((probes + 1));;
            rm\ *) removals=$((removals + 1));;
        esac
    done < "$effects"
    [[ "$probes" == 1 && "$removals" == 10 ]] || fail 'combined cleanup duplicated or omitted effects'
    for path in "${artifacts[@]}" "$ubuntu"; do
        [[ ! -e "$work/$path" ]] || fail 'combined cleanup retained a local artifact'
    done
    [[ ! -e "$sandbox/vm" ]] || fail 'combined cleanup retained the VM directory'
    expect_shared_intact
}

# Verifies that an empty optional name omits only its operand and retains
# every other cleanup effect.
test_empty_operand() {
    local variable=$1 goal=$2 disabled path
    case "$variable" in
        QEMU_IMAGE) disabled=yanet-test.qcow2;;
        CLOUD_INIT_AUTOLOGIN_ISO) disabled=cloud-init-autologin.iso;;
        UBUNTU_IMAGE) disabled=$ubuntu;;
    esac
    seed_local
    run_make "$image" "$goal" "$variable=" || fail 'empty optional artifact blocked cleanup'
    {
        write_scan_effect "$image"
        for path in "${artifacts[@]}"; do
            [[ "$path" != "$disabled" ]] || continue
            write_effect rm "$image" -f -- "$work/$path"
        done
        if [[ "$goal" == clean-all && "$variable" != UBUNTU_IMAGE ]]; then
            write_effect rm "$image" -f -- "$work/$ubuntu"
        fi
        write_effect rm "$image" -rf "$sandbox/vm"
        write_effect go "$image" clean -testcache
    } > "$fixture/expected-effects"
    cmp -s "$fixture/expected-effects" "$effects" || fail 'empty optional name changed remaining cleanup effects'
    for path in "${artifacts[@]}" "$ubuntu"; do
        if [[ "$path" == "$disabled" || ( "$path" == "$ubuntu" && "$goal" == clean ) ]]; then
            [[ -f "$work/$path" ]] || fail 'disabled or ineligible artifact removed'
        else
            [[ ! -e "$work/$path" ]] || fail 'remaining eligible artifact retained'
        fi
    done
    [[ ! -e "$sandbox/vm" ]] || fail 'empty optional name retained independent VM artifacts'
    expect_shared_intact
}

# Verifies that each cleanup stage preflights its operands without repeating
# an earlier safe stage.
test_mixed_cleanup() {
    local graph=$1 action=$2 path
    local args=()
    case "$graph" in
        clean-first) args=(clean "${alias_goal[@]}");;
        clean-ready)
            run_make "$image" check-image || fail 'owned input validation failed'
            expect_no_effects
            args=(-o check-image clean "${alias_goal[@]}");;
        all-first) args=("${alias_goal[@]}" clean);;
        reused) args=("${reuse_goal[@]}");;
    esac
    if [[ "$action" == clean ]]; then
        test_combined_cleanup "${args[@]}"
        return
    fi
    seed_local
    if run_make "$image" "${args[@]}" "UBUNTU_IMAGE=$shared/$ubuntu"; then
        fail 'mixed cleanup accepted a shared-neighbor Ubuntu operand'
    fi
    if [[ "$graph" == all-first || ! -s "$effects" ]]; then
        expect_no_effects
        [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]] || fail 'all-clean preflight occurred after removal'
    else
        {
            write_scan_effect "$image"
            for path in "${artifacts[@]}"; do write_effect rm "$image" -f -- "$work/$path"; done
            write_effect rm "$image" -rf "$sandbox/vm"
            write_effect go "$image" clean -testcache
        } > "$fixture/expected-effects"
        cmp -s "$fixture/expected-effects" "$effects" || fail 'later Ubuntu refusal duplicated or changed the prior safe cleanup'
        [[ ! -e "$work/test_run.log" && ! -e "$sandbox/vm" && -f "$work/$ubuntu" ]] || fail 'mixed cleanup lost the independent-clean contract'
    fi
    expect_shared_intact
}

# Verifies that every symlink hop retains aliases and rechecks neighbors in
# the resolved parent.
test_symlink_image() {
    local kind=$1 goal=$2 action=$3 selected
    seed_local
    mkdir "$fixture/image aliases"
    selected="$fixture/image aliases/base-link.qcow2"
    if [[ "$kind" == absolute ]]; then
        ln -s "$image" "$selected"
    else
        ln -s '../shared image/yanet-test.qcow2' "$selected"
    fi
    if [[ "$action" == refuse ]]; then
        if run_make "$selected" "$goal" "QEMU_IMAGE=$shared/yanet-test-booted-v3.qcow2"; then
            fail 'operand in resolved target parent accepted'
        fi
        expect_no_effects
        [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]] || fail 'resolved-parent refusal happened after removal'
    else
        /bin/rm -- "$work/yanet-test.qcow2" "$work/cloud-init-autologin.iso"
        ln -s "$selected" "$work/yanet-test.qcow2"
        ln "$image" "$work/cloud-init-autologin.iso"
        run_make "$selected" "$goal" || fail 'valid symlink image cleanup rejected'
        expect_cleanup "$selected"
        [[ -L "$work/yanet-test.qcow2" && "$work/cloud-init-autologin.iso" -ef "$image" ]] || fail 'symlink-image aliases removed'
        [[ ! -e "$work/test_run.log" && ! -e "$sandbox/vm" ]] || fail 'independent symlink-image cleanup suppressed'
    fi
    [[ -L "$selected" && "$selected" -ef "$image" ]] || fail 'selected image symlink changed'
    expect_shared_intact
}

# Verifies that help prints literal configuration data without validation or
# effectful tools.
test_help_path() {
    local mode=$1 selected expected
    case "$mode" in
        unset) selected='<unset>'; run_make "$selected" help || fail 'unset-image help failed'; selected=;;
        empty) selected=; run_make "$selected" help || fail 'empty-image help failed';;
        *)
            selected="$shared/\$(shell go help-expanded) \"; go injected; # %.qcow2"
            if [[ "$mode" == command-line ]]; then
                run_make '<unset>' help "YANET_QEMU_IMAGE=$selected" || fail 'command-line help failed'
            else
                run_make "$selected" help || fail 'exported-image help failed'
            fi
            ;;
    esac
    expect_no_effects
    expected=$(printf '  YANET_QEMU_IMAGE: %s' "$selected")
    [[ "$make_output" == *"$expected"* ]] || fail 'help did not render the literal image path'
    printf 'help literal data: %s\n' "$expected"
}

# Verifies that parse-time host detection never evaluates exported image
# expressions, including during dry runs.
test_literal_parse() {
    local origin=$1
    image="$shared/\$(shell go parse-expanded).qcow2"
    /bin/cp "$sandbox/base.expected" "$image"
    case "$origin" in
        command-line) run_make '<unset>' -n help "YANET_QEMU_IMAGE=$image" || fail 'literal command-line dry run failed';;
        environment-with-e) run_make "$image" -e -n help || fail 'literal environment-with-e dry run failed';;
        environment) run_make "$image" -n help || fail 'literal exported dry run failed';;
    esac
    expect_no_effects
    expect_shared_intact
}

# Verifies that relative parent aliases stay protected despite directory
# search paths or option-like names.
test_relative_shared_operand() {
    local variable=$1 spelling=$2 alias=shared-link path fixture_cdpath=.
    seed_local
    [[ "$spelling" != leading-dash ]] || alias=-shared
    ln -s "$shared" "$work/$alias"
    case "$variable" in
        QEMU_IMAGE) path=yanet-test-booted-v3.qcow2;;
        CLOUD_INIT_AUTOLOGIN_ISO) path=cloud-init-autologin.iso;;
        UBUNTU_IMAGE) path=$ubuntu;;
    esac
    if run_make "$image" clean-all "$variable=$alias/$path"; then fail 'relative shared-neighbor operand accepted'; fi
    expect_no_effects
    [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]] || fail 'relative operand rejected after removal'
    expect_shared_intact
}

# Verifies that absolute and relative links through a newline-terminated hop
# protect every parent's regular neighbors before cleanup effects.
test_newline_symlink_chain() {
    local kind=$1 goal=$2 neighbor=$3 selected intermediate path status=0
    local selected_link intermediate_link directory name
    seed_local
    /bin/cp "$work/yanet-test.qcow2" "$fixture/local.expected"
    /bin/cp "$sandbox/vm/log" "$fixture/vm.expected"
    mkdir "$fixture/first" "$fixture/second"
    selected="$fixture/first/base-link.qcow2"
    intermediate="$fixture/second/hop"$'\n\n'
    if [[ "$kind" == absolute ]]; then
        ln -s "$image" "$intermediate"
        ln -s "$intermediate" "$selected"
    else
        ln -s '../shared image/yanet-test.qcow2' "$intermediate"
        ln -s "../second/hop"$'\n\n' "$selected"
    fi
    [[ "$selected" -ef "$image" ]] || fail 'newline symlink chain is invalid'
    selected_link=$(readlink -n "$selected" && printf .)
    intermediate_link=$(readlink -n "$intermediate" && printf .)
    for directory in "$fixture/first" "$fixture/second"; do
        for name in neighbor-cache.qcow2 neighbor-cache.qcow2.sha256; do
            /bin/cp "$sandbox/neighbor.expected" "$directory/$name"
        done
    done
    case "$neighbor" in
        final) path="$shared/yanet-test-booted-v3.qcow2";;
        selected) path="$fixture/first/neighbor-cache.qcow2";;
        intermediate) path="$fixture/second/neighbor-cache.qcow2.sha256";;
    esac
    run_make "$selected" "$goal" "QEMU_IMAGE=$path" || status=$?
    for directory in "$fixture/first" "$fixture/second"; do
        for name in neighbor-cache.qcow2 neighbor-cache.qcow2.sha256; do
            cmp -s "$sandbox/neighbor.expected" "$directory/$name" || fail "neighbor bytes changed: $directory/$name"
        done
    done
    [[ "$status" != 0 ]] || fail 'newline symlink chain accepted a protected parent operand'
    expect_no_effects
    [[ -L "$selected" && -L "$intermediate" && "$selected" -ef "$image" ]] || fail 'newline symlink chain changed'
    [[ "$(readlink -n "$selected" && printf .)" == "$selected_link" && "$(readlink -n "$intermediate" && printf .)" == "$intermediate_link" ]] || fail 'newline symlink targets changed'
    for path in "${artifacts[@]}" "$ubuntu"; do
        cmp -s "$fixture/local.expected" "$work/$path" || fail "independent local bytes changed: $path"
    done
    cmp -s "$fixture/vm.expected" "$sandbox/vm/log" || fail 'independent VM bytes changed'
    expect_shared_intact
}

# Verifies that canonical directory captures retain newline bytes at parent
# and ancestry boundaries.
test_newline_directory() {
    local kind=$1 selected=$image parent path
    seed_local
    parent="$fixture/parent"$'\n\n'
    mkdir "$parent"
    if [[ "$kind" == working ]]; then
        /bin/mv "$work" "$work"$'\n\n'
        work+=$'\n\n'
    elif [[ "$kind" == ancestor ]]; then
        /bin/mv "$shared" "$parent/shared image"
        shared="$parent/shared image"
    else
        /bin/mv "$shared" "$parent/shared image"$'\n\n'
        shared="$parent/shared image"$'\n\n'
    fi
    image="$shared/yanet-test.qcow2"
    ln -s "$shared" "$fixture/parent-alias"
    selected="$fixture/parent-alias/yanet-test.qcow2"
    if [[ "$kind" == refuse ]]; then
        if run_make "$selected" clean-all "CLOUD_INIT_AUTOLOGIN_ISO=$fixture/parent-alias/cloud-init-autologin.iso"; then
            fail 'newline canonical parent bypassed protection'
        fi
        expect_no_effects
        [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]] || fail 'newline directory rejected after removal'
    else
        run_make "$selected" clean-all || fail 'newline directory blocked independent cleanup'
        expect_cleanup "$selected"
        for path in "${artifacts[@]}" "$ubuntu"; do
            [[ ! -e "$work/$path" ]] || fail 'newline directory suppressed local cleanup'
        done
        [[ ! -e "$sandbox/vm" ]] || fail 'newline directory retained independent VM artifacts'
    fi
    expect_shared_intact
}

# Verifies that missing independent artifacts are no-ops while dangling
# symlinks remain protected.
test_absent_operand() {
    local variable=$1 kind=$2 path
    seed_local
    path="$fixture/absent parent/artifact"
    case "$kind" in
        missing-file) mkdir "$fixture/absent parent";;
        dangling-symlink)
            path="$fixture/dangling artifact"
            ln -s "$fixture/absent parent/artifact" "$path";;
    esac
    run_make "$image" clean-all "$variable=$path" || fail 'absent independent artifact blocked cleanup'
    expect_cleanup "$image"
    if [[ "$kind" == dangling-symlink ]]; then
        [[ -L "$path" && ! -e "$path" ]] || fail 'dangling symlink was removed'
    else
        [[ ! -e "$path" && ! -L "$path" ]] || fail 'absent operand changed'
    fi
    [[ ! -e "$work/test_run.log" && ! -e "$sandbox/vm" ]] || fail 'absent operand suppressed independent cleanup'
    expect_shared_intact
}

# Verifies that local media names cannot replace shared validation or add
# debug prerequisites.
test_media_collision() {
    local variable=$1 target=$2 spelling=$3 value=$2 selected status path
    local goals=("$target")
    [[ "$target" != check-image ]] || goals+=(prepare-vm)
    [[ "$spelling" != split ]] || value="$work/local $target"
    for selected in "$image" "$shared/missing.qcow2"; do
        status=0
        run_make "$selected" "${goals[@]}" "$variable=$value" || status=$?
        if [[ "$selected" != "$image" || "$target" == debug-vm* ]]; then
            [[ "$status" == 2 ]] || fail 'invalid input or shared debug did not reject'
        else
            [[ "$status" == 0 ]] || fail 'valid shared preparation rejected'
        fi
        expect_no_effects
        for path in "${artifacts[@]}" "$ubuntu" cloud-init-tmp; do
            [[ ! -e "$work/$path" ]] || fail 'shared target created local media'
        done
        expect_shared_intact
        printf 'shared collision argv/env: %s=%q selected=%q exit=%s effects=none\n' "$variable" "$value" "$selected" "$status"
    done
}

# Verifies that Make references expand once into quoted cleanup data,
# including escaped dollars.
test_make_operand() {
    local variable=$1 goal=$2 spelling=$3 name disabled path shadow value operand removal
    seed_local
    case "$variable" in
        QEMU_IMAGE) name=independent.qcow2; disabled=yanet-test.qcow2;;
        CLOUD_INIT_AUTOLOGIN_ISO) name=independent.iso; disabled=cloud-init-autologin.iso;;
        UBUNTU_IMAGE) name=independent.img; disabled=$ubuntu;;
    esac
    [[ "$spelling" != dollars ]] || name='literal $cash-'"$name"
    path="$work/$name"
    shadow="$work/\$(CURDIR)/$name"
    mkdir "$work/\$(CURDIR)"
    /bin/cp "$sandbox/neighbor.expected" "$path"
    /bin/cp "$sandbox/neighbor.expected" "$shadow"
    value='$(CURDIR)/'"${name//\$/\$\$}"
    run_make "$image" "$goal" "$variable=$value" || fail 'expanded artifact cleanup rejected'
    {
        write_scan_effect "$image"
        for operand in "${artifacts[@]}"; do
            removal="$work/$operand"
            [[ "$operand" != "$disabled" ]] || removal=$path
            write_effect rm "$image" -f -- "$removal"
        done
        if [[ "$goal" == clean-all ]]; then
            removal="$work/$ubuntu"
            [[ "$variable" != UBUNTU_IMAGE ]] || removal=$path
            write_effect rm "$image" -f -- "$removal"
        fi
        write_effect rm "$image" -rf "$sandbox/vm"
        write_effect go "$image" clean -testcache
    } > "$fixture/expected-effects"
    cmp -s "$fixture/expected-effects" "$effects" || fail 'expanded artifact changed the complete cleanup sequence'
    expect_cleanup "$image"
    [[ ! -e "$path" && -f "$work/$disabled" ]] || fail 'cleanup did not select the expanded artifact'
    cmp -s "$sandbox/neighbor.expected" "$shadow" || fail 'literal Make-expression shadow changed'
    expect_shared_intact
}

# Verifies that expanded shared-parent operands are rejected before effects
# and expanded aliases survive cleanup.
test_make_protected_operand() {
    local variable=$1 kind=$2 name path value status=0
    seed_local
    if [[ "$kind" == neighbor ]]; then
        case "$variable" in
            QEMU_IMAGE) name=yanet-test-booted-v3.qcow2;;
            CLOUD_INIT_AUTOLOGIN_ISO) name=cloud-init-autologin.iso;;
            UBUNTU_IMAGE) name=$ubuntu;;
        esac
        value='$(CURDIR)/../shared image/'"$name"
        run_make "$image" clean-all "$variable=$value" || status=$?
        [[ "$status" == 2 ]] || fail 'expanded shared-neighbor operand accepted'
        expect_no_effects
        [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]] || fail 'expanded operand rejected after removal'
    else
        path="$work/expanded-alias"
        if [[ "$variable" == CLOUD_INIT_AUTOLOGIN_ISO ]]; then ln -s "$image" "$path"; else ln "$image" "$path"; fi
        value='$(CURDIR)/expanded-alias'
        run_make "$image" clean-all "$variable=$value" || fail 'expanded alias blocked independent cleanup'
        {
            write_scan_effect "$image"
            for name in "${artifacts[@]}"; do
                case "$variable:$name" in
                    QEMU_IMAGE:yanet-test.qcow2|CLOUD_INIT_AUTOLOGIN_ISO:cloud-init-autologin.iso) continue;;
                esac
                write_effect rm "$image" -f -- "$work/$name"
            done
            [[ "$variable" == UBUNTU_IMAGE ]] || write_effect rm "$image" -f -- "$work/$ubuntu"
            write_effect rm "$image" -rf "$sandbox/vm"
            write_effect go "$image" clean -testcache
        } > "$fixture/expected-effects"
        cmp -s "$fixture/expected-effects" "$effects" || fail 'expanded alias changed the complete cleanup sequence'
        expect_cleanup "$image"
        [[ "$path" -ef "$image" && ! -e "$work/test_run.log" && ! -e "$sandbox/vm" ]] || fail 'expanded alias removed or independent cleanup suppressed'
    fi
    expect_shared_intact
}

# Verifies that a failed, empty-output scan preserves local files and
# recursive shared aliases.
test_scan_failure() {
    local goal=$1 missing=find path status=0
    seed_local
    mkdir "$sandbox/vm/nested"
    ln "$image" "$sandbox/vm/nested/input-alias.qcow2"
    /bin/cp "$work/yanet-test.qcow2" "$fixture/local.expected"
    /bin/cp "$sandbox/vm/log" "$fixture/vm.expected"
    run_make "$image" "$goal" || status=$?
    [[ "$status" != 0 ]] || fail 'cleanup accepted the failed scan'
    write_scan_effect "$image" > "$fixture/expected-effects"
    cmp -s "$fixture/expected-effects" "$effects" || fail 'failed scan launched removal or cache clearing'
    for path in "${artifacts[@]}" "$ubuntu"; do
        cmp -s "$fixture/local.expected" "$work/$path" || fail "failed scan changed local artifact: $path"
    done
    cmp -s "$fixture/vm.expected" "$sandbox/vm/log" || fail 'failed scan changed VM logs'
    [[ -d "$sandbox/vm/nested" && "$sandbox/vm/nested/input-alias.qcow2" -ef "$image" ]] || fail 'failed scan removed the recursive input alias'
    expect_shared_intact
    printf 'scan failure exit=%s; only effect: %s\n' "$status" "$(< "$effects")"
}

# Verifies that a comparison error aborts cleanup before local removal and
# preserves a newline-ended hardlink to the shared input.
test_comparison_failure() {
    local goal=$1 comparison_error_path="$sandbox/vm/input-alias.qcow2"$'\n\n' path status=0 expected_fault
    seed_local || fail 'comparison fixture seeding failed'
    printf 'local artifact\n' > "$fixture/local.expected" || fail 'local expectation creation failed'
    printf 'local VM artifact\n' > "$fixture/vm.expected" || fail 'VM expectation creation failed'
    ln "$image" "$comparison_error_path" || fail 'comparison hardlink creation failed'
    for path in "${artifacts[@]}" "$ubuntu"; do
        cmp -s "$fixture/local.expected" "$work/$path" || fail "comparison fixture lacks local bytes: $path"
    done
    cmp -s "$fixture/vm.expected" "$sandbox/vm/log" || fail 'comparison fixture lacks VM bytes'
    [[ "$comparison_error_path" -ef "$image" ]] || fail 'comparison fixture hardlink is invalid'
    expect_shared_intact
    run_make "$image" "$goal" || status=$?
    [[ "$status" != 0 ]] || fail 'cleanup accepted the comparison error'
    write_scan_effect "$image" > "$fixture/expected-effects"
    cmp -s "$fixture/expected-effects" "$effects" || fail 'comparison error launched removal or cache clearing'
    expected_fault=$(write_effect test "$image" "$comparison_error_path" -ef "$image")
    printf '%s status=2\n' "$expected_fault" > "$fixture/expected-comparison-errors"
    cmp -s "$fixture/expected-comparison-errors" "$comparison_errors" || fail 'exact comparison fault was not observed once'
    for path in "${artifacts[@]}" "$ubuntu"; do
        cmp -s "$fixture/local.expected" "$work/$path" || fail "comparison error changed local bytes: $path"
    done
    cmp -s "$fixture/vm.expected" "$sandbox/vm/log" || fail 'comparison error changed VM bytes'
    [[ "$comparison_error_path" -ef "$image" ]] || fail 'comparison error removed the input alias'
    cmp -s "$sandbox/base.expected" "$comparison_error_path" || fail 'comparison error changed alias bytes'
    expect_shared_intact
    printf 'comparison failure exit=%s; fault: %s\n' "$status" "$(< "$comparison_errors")"
}

review_failures=0
for goal in clean clean-all; do
    run_review_case "$goal fails closed on empty-output scan failure with a recursive input alias" test_scan_failure "$goal"
    run_review_case "$goal fails closed on external comparison error with a VM-root hardlink" test_comparison_failure "$goal"
done
for variable in QEMU_IMAGE CLOUD_INIT_AUTOLOGIN_ISO UBUNTU_IMAGE; do
    for target in check-image prepare-vm; do
        for spelling in simple split; do
            run_review_case "shared $target ignores $variable $spelling collision with valid/invalid input" test_media_collision "$variable" "$target" "$spelling"
        done
    done
    for goal in clean clean-all; do
        [[ "$variable" != UBUNTU_IMAGE || "$goal" == clean-all ]] || continue
        run_review_case "$goal expands $variable Make reference and preserves its literal shadow" test_make_operand "$variable" "$goal" reference
    done
    run_review_case "clean-all expands escaped literal dollars in $variable without shell interpretation" test_make_operand "$variable" clean-all dollars
    for kind in neighbor alias; do
        run_review_case "expanded $variable $kind retains shared protection and independent cleanup" test_make_protected_operand "$variable" "$kind"
    done
done
for target in debug-vm debug-vm-ssh; do
    for spelling in simple split; do
        run_review_case "shared $target rejects QEMU_IMAGE $spelling collision before inherited media prerequisites" test_media_collision QEMU_IMAGE "$target" "$spelling"
    done
done
for variable in QEMU_IMAGE CLOUD_INIT_AUTOLOGIN_ISO UBUNTU_IMAGE; do
    for goal in clean clean-all; do
        run_review_case "$goal skips empty $variable and preserves exact remaining effects" test_empty_operand "$variable" "$goal"
    done
done
alias_goal=('--eval=distclean: clean-all' distclean)
reuse_goal=('--eval=distclean: clean clean-all' distclean)
if [[ $("$make_bin" --help) != *--eval=* ]]; then
    printf 'distclean: clean-all\n' > "$sandbox/alias.mk"
    printf 'distclean: clean clean-all\n' > "$sandbox/reuse.mk"
    alias_goal=(-f "$sandbox/alias.mk" distclean)
    reuse_goal=(-f "$sandbox/reuse.mk" distclean)
fi
printf 'Indirect cleanup arguments:'; printf ' %q' "${alias_goal[@]}"; printf '\n'
run_review_case 'indirect clean-all removes Ubuntu with exactly one cleanup pass' test_combined_cleanup "${alias_goal[@]}"
run_review_case 'indirect clean-all refuses a shared-neighbor Ubuntu override before effects' test_shared_operand UBUNTU_IMAGE spaces "${alias_goal[@]}"
for graph in clean-first clean-ready all-first reused; do
    run_review_case "$graph mixed cleanup removes Ubuntu without repeating clean" test_mixed_cleanup "$graph" clean
    run_review_case "$graph mixed cleanup refuses protected Ubuntu before unsafe effects" test_mixed_cleanup "$graph" refuse
done
for origin in environment environment-with-e command-line; do
    run_review_case "literal $origin image expression is inert before parse-time host detection" test_literal_parse "$origin"
done
for variable in QEMU_IMAGE CLOUD_INIT_AUTOLOGIN_ISO UBUNTU_IMAGE; do
    for spelling in relative leading-dash; do
        run_review_case "$variable $spelling shared-parent alias with CDPATH fails before removal" test_relative_shared_operand "$variable" "$spelling"
    done
    for kind in missing-parent missing-file dangling-symlink; do
        run_review_case "$variable $kind preserves independent cleanup and dangling aliases" test_absent_operand "$variable" "$kind"
    done
    run_review_case "$variable newline-containing independent parent is one deletion operand" test_independent_operand "$variable" newline
done
for kind in absolute relative; do
    run_review_case "$kind newline symlink chain protects the final parent's neighbors" test_newline_symlink_chain "$kind" clean-all final
    for goal in clean clean-all; do
        for neighbor in selected intermediate; do
            run_review_case "$kind newline symlink chain $goal refuses $neighbor parent neighbors before effects" test_newline_symlink_chain "$kind" "$goal" "$neighbor"
        done
    done
done
for kind in parent ancestor working refuse; do
    run_review_case "newline canonical directory $kind retains bytes and cleanup boundaries" test_newline_directory "$kind"
done
for kind in input hardlink; do
    run_review_case "VM-root regular file preserves $kind and independent cleanup" test_vm_root_file "$kind"
done
for variable in QEMU_IMAGE CLOUD_INIT_AUTOLOGIN_ISO UBUNTU_IMAGE; do
    for spelling in spaces parent-alias; do
        run_review_case "$variable shared-neighbor override with $spelling fails before removal" test_shared_operand "$variable" "$spelling"
    done
    run_review_case "$variable independent space-containing override is one deletion operand" test_independent_operand "$variable"
done
for origin in command-line environment-with-e; do
    run_review_case "literal $origin image path reaches Go unchanged" test_literal_origin "$origin"
    for initializer in CLOUD_INIT_AUTOLOGIN_ISO UBUNTU_IMAGE; do
        run_review_case "literal $origin image stays inert while $initializer expands during startup" test_literal_origin "$origin" "$initializer"
    done
done
for repetition in {1..10}; do
    if (( repetition % 2 )); then
        run_review_case "combined -j4 clean/clean-all invocation $repetition uses one cleanup pass" test_combined_cleanup clean clean-all
    else
        run_review_case "combined -j4 clean-all/clean invocation $repetition uses one cleanup pass" test_combined_cleanup clean-all clean
    fi
done
for kind in absolute relative; do
    for goal in clean clean-all; do
        run_review_case "$kind symlink-image $goal preserves aliases and independent cleanup" test_symlink_image "$kind" "$goal" clean
    done
    run_review_case "$kind symlink-image refuses an operand in the resolved target parent" test_symlink_image "$kind" clean-all refuse
done
for mode in unset empty environment command-line; do
    run_review_case "help renders $mode image data without execution" test_help_path "$mode"
done
[[ "$review_failures" == 0 ]] || { printf 'FAIL: %s review regressions\n' "$review_failures" >&2; exit 1; }

for os in Darwin Linux; do
    new_case "shared preparation on $os without installed dependencies"
    touch "$work/prepare-vm" "$work/check-image" "$work/test" "$work/test-main" "$work/test-run"
    for target in prepare-vm all test test-main test-run; do
        run_make "$image" "$target" TEST='TestExample|TestOther' || fail "$target rejected valid image"
        case "$target" in
            prepare-vm) expect_no_effects;;
            all|test) expect_go "$image" test -count=1 -v ./main/...;;
            test-main) expect_go "$image" test -count=1 -v -failfast ./main/...;;
            test-run) expect_go "$image" test -count=1 -v -run 'TestExample|TestOther' ./...;;
        esac
        for path in "${artifacts[@]}" "$ubuntu" cloud-init-tmp; do
            [[ ! -e "$work/$path" ]] || fail "local bootstrap artifact appeared: $path"
        done
        expect_shared_intact
    done
    printf 'PASS: %s\n' "$case_name"
done

new_case 'exported path punctuation is data, not Make or shell syntax'
image="$shared/\$(go injected) ' \" ;.qcow2"
/bin/cp "$sandbox/base.expected" "$image"
run_make "$image" test-main || fail 'literal path rejected'
expect_go "$image" test -count=1 -v -failfast ./main/...
expect_shared_intact
printf 'PASS: %s\n' "$case_name"

for invalid in relative missing directory unreadable whitespace; do
    new_case "invalid $invalid override blocks every host test entrypoint"
    case "$invalid" in
        relative) selected=yanet-test.qcow2; printf 'existing relative file\n' > "$work/$selected";;
        missing) selected="$shared/missing.qcow2";;
        directory) selected="$shared";;
        unreadable)
            selected="$shared/unreadable.qcow2"
            printf 'unreadable fixture\n' > "$selected"
            chmod 000 "$selected"
            [[ ! -r "$selected" ]] || fail 'fixture remains readable (run as an unprivileged user)'
            ;;
        whitespace) selected='   ';;
    esac
    touch "$work/prepare-vm" "$work/test" "$work/test-main" "$work/test-run"
    for target in prepare-vm all test test-main test-run clean clean-all; do
        if run_make "$selected" "$target" TEST=TestExample; then fail "$target accepted invalid override"; fi
        expect_no_effects
    done
    printf 'PASS: %s\n' "$case_name"
done

new_case 'shared debug rejects before bootstrap, even with invalid input'
for selected in "$image" "$shared/missing.qcow2" '   '; do
    for target in debug-vm debug-vm-ssh; do
        touch "$work/$target"
        if run_make "$selected" "$target"; then fail 'debug accepted external mode'; fi
        expect_no_effects
    done
done
printf 'PASS: %s\n' "$case_name"

for target in clean clean-all; do
    new_case "$target preserves same-name shared input and neighbors, removes independent local artifacts"
    seed_local
    run_make "$image" "$target" || fail 'independent cleanup failed'
    expect_cleanup "$image"
    expect_shared_intact
    for path in "${artifacts[@]}"; do [[ ! -e "$work/$path" ]] || fail "local artifact retained: $path"; done
    [[ ! -e "$sandbox/vm" ]] || fail 'independent VM directory retained'
    if [[ "$target" == clean ]]; then
        [[ -f "$work/$ubuntu" ]] || fail 'clean removed Ubuntu download'
    else
        [[ ! -e "$work/$ubuntu" ]] || fail 'clean-all retained Ubuntu download'
    fi
    printf 'PASS: %s\n' "$case_name"

    new_case "$target preserves symlink and hardlink input aliases and neighboring snapshot aliases"
    seed_local
    /bin/rm -- "$work/yanet-test.qcow2" "$work/cloud-init-autologin.iso" "$work/qemu.log" "$work/$ubuntu" "$work/yanet-test-booted-v3.qcow2"
    ln "$image" "$work/yanet-test.qcow2"
    ln -s "$image" "$work/cloud-init-autologin.iso"
    ln "$image" "$work/qemu.log"
    ln "$image" "$work/$ubuntu"
    ln -s "$shared/yanet-test-booted-v3.qcow2" "$work/yanet-test-booted-v3.qcow2"
    ln -s "$shared" "$fixture/shared-alias"
    selected="$fixture/shared-alias/./yanet-test.qcow2"
    run_make "$selected" "$target" || fail 'alias-safe cleanup failed'
    expect_cleanup "$selected"
    for path in yanet-test.qcow2 cloud-init-autologin.iso qemu.log "$ubuntu"; do
        [[ "$work/$path" -ef "$image" ]] || fail "input alias removed: $path"
    done
    [[ -L "$work/yanet-test-booted-v3.qcow2" ]] || fail 'neighbor alias removed'
    [[ ! -e "$work/test_run.log" && ! -e "$work/yanet-test-baseline-test.qcow2" ]] || fail 'independent local cleanup suppressed'
    expect_shared_intact
    printf 'PASS: %s\n' "$case_name"
done

for overlap in local local-parent-alias resolved-local-symlink vm vm-parent-alias vm-symlink-parent vm-in-shared-parent; do
    new_case "cleanup fails closed for $overlap shared-directory overlap"
    seed_local
    case "$overlap" in
        local) selected="$work/yanet-test.qcow2";;
        local-parent-alias)
            ln -s "$work" "$fixture/work-alias"
            [[ "$fixture/work-alias" -ef "$work" ]] || fail 'local directory metadata alias fixture is invalid'
            selected="$fixture/work-alias/./yanet-test.qcow2";;
        resolved-local-symlink)
            ln -s ../work/yanet-test.qcow2 "$shared/local-link.qcow2"
            selected="$shared/local-link.qcow2";;
        vm) printf 'base\n' > "$sandbox/vm/base.qcow2"; selected="$sandbox/vm/base.qcow2";;
        vm-parent-alias)
            printf 'base\n' > "$sandbox/vm/base.qcow2"
            ln -s "$sandbox/vm" "$fixture/vm-alias"
            selected="$fixture/vm-alias/base.qcow2";;
        vm-symlink-parent)
            ln -s "$shared" "$sandbox/vm/shared-alias"
            selected="$sandbox/vm/shared-alias/yanet-test.qcow2";;
        vm-in-shared-parent)
            printf 'base\n' > "$sandbox/base.qcow2"
            selected="$sandbox/base.qcow2";;
    esac
    for target in clean clean-all; do
        if run_make "$selected" "$target"; then fail 'unsafe cleanup succeeded'; fi
        expect_no_effects
        [[ -f "$work/test_run.log" && -f "$sandbox/vm/log" ]] || fail 'overlap rejected after removal'
    done
    expect_shared_intact
    /bin/rm -rf -- "$sandbox/vm"
    printf 'PASS: %s\n' "$case_name"
done

new_case 'cleanup preserves a VM-directory symlink into shared storage'
seed_local
/bin/rm -rf -- "$sandbox/vm"
ln -s "$shared" "$sandbox/vm"
run_make "$image" clean-all || fail 'safe symlink-root cleanup rejected'
expect_cleanup "$image"
[[ -L "$sandbox/vm" && ! -e "$work/test_run.log" ]] || fail 'symlink root removed or all cleanup suppressed'
expect_shared_intact
/bin/rm -- "$sandbox/vm"
printf 'PASS: %s\n' "$case_name"

for alias in hardlink symlink; do
    new_case "cleanup preserves recursive VM directory containing a shared $alias alias"
    seed_local
    mkdir "$sandbox/vm/nested"
    if [[ "$alias" == hardlink ]]; then
        ln "$image" "$sandbox/vm/nested/base.qcow2"
    else
        ln -s "$shared" "$sandbox/vm/nested/shared-alias"
    fi
    run_make "$image" clean-all || fail 'alias-safe recursive cleanup failed'
    expect_cleanup "$image"
    [[ -e "$sandbox/vm/nested" && ! -e "$work/test_run.log" ]] || fail 'recursive alias removed or all cleanup suppressed'
    if [[ "$alias" == hardlink ]]; then
        [[ "$sandbox/vm/nested/base.qcow2" -ef "$image" ]] || fail 'recursive input alias removed'
    else
        [[ -L "$sandbox/vm/nested/shared-alias" ]] || fail 'recursive directory alias removed'
    fi
    expect_shared_intact
    /bin/rm -rf -- "$sandbox/vm"
    printf 'PASS: %s\n' "$case_name"
done

missing=none
for selected in '<unset>' ''; do
    for os in Darwin Linux; do
        new_case "local bootstrap with ${selected:-empty} override on $os"
        run_make "$selected" prepare-vm || fail 'local preparation failed'
        expect_local_prepare "$selected"
        [[ -f "$work/yanet-test.qcow2" && -f "$work/$ubuntu" && -f "$work/cloud-init-autologin.iso" ]] || fail 'local media not created'
        for target in test test-main test-run; do
            run_make "$selected" "$target" TEST=TestExample || fail 'local test failed'
            case "$target" in
                test) expect_local_go "$selected" test -count=1 -v ./main/...;;
                test-main) expect_local_go "$selected" test -count=1 -v -failfast ./main/...;;
                test-run) expect_go "$selected" test -count=1 -v -run TestExample ./...;;
            esac
        done
        for target in debug-vm debug-vm-ssh; do
            run_make "$selected" "$target" || fail 'local debug rejected'
            if [[ "$target" == debug-vm ]]; then expect_local_debug "$selected" debug; else expect_local_debug "$selected" ssh; fi
        done
        seed_local
        run_make "$selected" clean || fail 'local clean failed'
        expect_local_cleanup "$selected" clean
        for path in "${artifacts[@]}"; do [[ ! -e "$work/$path" ]] || fail "local clean retained $path"; done
        [[ ! -e "$sandbox/vm" && -f "$work/$ubuntu" ]] || fail 'local clean contract changed'
        run_make "$selected" clean-all || fail 'local clean-all failed'
        expect_local_cleanup "$selected" clean-all
        [[ ! -e "$work/$ubuntu" ]] || fail 'local clean-all retained download'
        expect_shared_intact
        printf 'PASS: %s\n' "$case_name"
    done
done
printf 'PASS: all shared/local Makefile matrix cases (%s fixtures)\n' "$case_number"
