#!/usr/bin/env bash
set -Eeuo pipefail

repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
go -C "$repo" build -o "$tmp/release-tool" ./tools/release
export MOCK_RELEASE_TOOL="$tmp/release-tool"

make_source_repo() {
    local path="$1" file="$2"
    mkdir -p -- "$path"
    git -C "$path" init -q
    git -C "$path" config user.name 'Build test'
    git -C "$path" config user.email 'build-test@example.invalid'
    printf 'fixture source\n' > "$path/$file"
    git -C "$path" add -- "$file"
    git -C "$path" commit -q -m 'fixture source'
}

make_fixture() {
    local root="$1"
    mkdir -p "$root"/{bin,meson_wrapfiles,wlroots_deps}
    cp "$repo/build.sh" "$root/"
    touch "$root/meson_wrapfiles/local.wrap" "$root/wlroots_deps/local.tar.xz"
    make_source_repo "$root/forks/cage" cage.c
    make_source_repo "$root/forks/wlroots-xtmapper" wlroots.c
    git -C "$root" init -q
    git -C "$root" config user.name 'Build test'
    git -C "$root" config user.email 'build-test@example.invalid'
    git -C "$root" update-index --add --cacheinfo "160000,$(git -C "$root/forks/cage" rev-parse HEAD),forks/cage"
    git -C "$root" update-index --add --cacheinfo "160000,$(git -C "$root/forks/wlroots-xtmapper" rev-parse HEAD),forks/wlroots-xtmapper"
    cat > "$root/bin/meson" <<'MOCK'
#!/usr/bin/env bash
echo "meson $*" >> "$MOCK_LOG"
if [[ "${1-}" == compile && "${MESON_FAIL-}" == compile ]]; then exit 31; fi
MOCK
    cat > "$root/bin/ninja" <<'MOCK'
#!/usr/bin/env bash
echo "ninja $*" >> "$MOCK_LOG"
if [[ "${1-}" == -C ]]; then
    shift 2
    if [[ "${1-}" == -t && "${2-}" == commands && "$3" == cage_xtmapper ]]; then
        printf 'cc -o cage_xtmapper cage_xtmapper.p/cage.c.o cage_xtmapper.p/seat.c.o -Wl,--as-needed -Wl,--start-group subprojects/wlroots/libwlroots-0.20.a -lffi -lm -Wl,--end-group\n'
    fi
fi
MOCK
    cat > "$root/bin/go" <<'MOCK'
#!/usr/bin/env bash
echo "go $*" >> "$MOCK_LOG"
if [[ "${1-}" == run && "${2-}" == ./tools/release ]]; then
    shift 2
    if [[ "${1-}" == record-build ]]; then
        echo "release $*" >> "$MOCK_LOG"
    else
        exec "$MOCK_RELEASE_TOOL" "$@"
    fi
fi
if [[ "${1-}" == build ]]; then
    [[ "${GO_FAIL-}" != build ]] || exit 32
    mkdir -p "$MOCK_ROOT/build/go"
    printf '#!/bin/sh\nexec true\n' > "$MOCK_ROOT/build/go/cage-xtmapper"
    chmod +x "$MOCK_ROOT/build/go/cage-xtmapper"
fi
MOCK
    chmod +x "$root/bin/meson" "$root/bin/ninja" "$root/bin/go"
}

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

# Invalid and duplicate flags fail before creating build output.
root="$tmp/cli"
make_fixture "$root"
export MOCK_LOG="$tmp/log" MOCK_ROOT="$root"
: > "$MOCK_LOG"
if PATH="$root/bin:$PATH" "$root/build.sh" --unknown >"$tmp/out" 2>&1; then
    fail 'unknown flag unexpectedly succeeded'
fi
[[ ! -e "$root/build" ]] || fail 'unknown flag changed build tree'
if PATH="$root/bin:$PATH" "$root/build.sh" --dry-run --dry-run >"$tmp/out" 2>&1; then
    fail 'duplicate --dry-run unexpectedly succeeded'
fi
[[ ! -e "$root/build" ]] || fail 'duplicate flag changed build tree'

# Dry-run assembles committed fork sources and configures Meson out of tree, from any cwd.
: > "$MOCK_LOG"
(cd / && PATH="$root/bin:$PATH" "$root/build.sh" --dry-run >/dev/null)
grep -q '^meson setup ' "$MOCK_LOG"
grep -q '^meson setup .* --buildtype=release ' "$MOCK_LOG" || fail 'fresh Meson setup did not request release buildtype'
grep -q -- '-Dwlroots:buildtype=release' "$MOCK_LOG" || fail 'wlroots did not receive release buildtype'
grep -q -- '-Dforce_fallback_for=wayland-server,wayland-client,xkbcommon,pixman-1' "$MOCK_LOG" || fail 'bundled native dependencies were not selected'
if grep -q '^meson compile ' "$MOCK_LOG"; then fail 'dry-run invoked compile'; fi
if grep -q '^meson install ' "$MOCK_LOG"; then fail 'dry-run invoked install'; fi
if grep -q '^go build ' "$MOCK_LOG"; then fail 'dry-run invoked Go'; fi
[[ -f "$root/build/source/cage-0.3.0/cage.c" ]] || fail 'Cage source was not assembled'
[[ -f "$root/build/source/cage-0.3.0/subprojects/wlroots/wlroots.c" ]] || fail 'wlroots source was not placed under Cage subprojects'
[[ -d "$root/build/source/cage-0.3.0/subprojects/wlroots/subprojects/packagefiles" ]] || fail 'dependency archive location is missing'
[[ ! -e "$root/build/source/cage-0.3.0/.git" ]] || fail 'staged source unexpectedly contains Git metadata'
grep -q "setup $root/build/meson $root/build/source/cage-0.3.0" "$MOCK_LOG" || fail 'Meson build directory is not outside the staged source tree'

# Reconfiguration must reset an existing debug build to release too.
mkdir -p "$root/build/meson/meson-private"
touch "$root/build/meson/meson-private/coredata.dat"
: > "$MOCK_LOG"
PATH="$root/bin:$PATH" "$root/build.sh" --dry-run >/dev/null
grep -q '^meson setup --reconfigure .* --buildtype=release ' "$MOCK_LOG" || fail 'Meson reconfigure did not request release buildtype'
grep -q -- '-Dwlroots:buildtype=release' "$MOCK_LOG" || fail 'wlroots did not receive release buildtype'

# Debug is opt-in and may be combined with dry-run in either order.
for flags in '--debug --dry-run' '--dry-run --debug'; do
    : > "$MOCK_LOG"
    read -r -a debug_args <<< "$flags"
    PATH="$root/bin:$PATH" "$root/build.sh" "${debug_args[@]}" >/dev/null
    grep -q -- '--buildtype=debug' "$MOCK_LOG" || fail 'debug mode was not requested'
    grep -q -- '-Dwlroots:buildtype=debug' "$MOCK_LOG" || fail 'wlroots debug mode was not requested'
done
: > "$MOCK_LOG"
PATH="$root/bin:$PATH" "$root/build.sh" --dry-run >/dev/null
grep -q -- '-Dwlroots:buildtype=release' "$MOCK_LOG" || fail 'default did not restore wlroots release mode'
if PATH="$root/bin:$PATH" "$root/build.sh" --debug --debug >/dev/null 2>&1; then
    fail 'duplicate --debug unexpectedly succeeded'
fi

# A changed fork commit must first be recorded by the parent gitlink.
printf 'updated fixture source\n' > "$root/forks/cage/cage.c"
git -C "$root/forks/cage" add cage.c
git -C "$root/forks/cage" commit -q -m 'updated fixture source'
: > "$MOCK_LOG"
if PATH="$root/bin:$PATH" "$root/build.sh" --dry-run >"$tmp/out" 2>&1; then
    fail 'build unexpectedly accepted a commit different from the parent gitlink'
fi
grep -q 'update the parent gitlink before building' "$tmp/out"
[[ ! -s "$MOCK_LOG" ]] || fail 'Meson ran before the parent gitlink was updated'
git -C "$root" update-index --cacheinfo "160000,$(git -C "$root/forks/cage" rev-parse HEAD),forks/cage"
: > "$MOCK_LOG"
PATH="$root/bin:$PATH" "$root/build.sh" --dry-run >/dev/null
grep -q '^meson setup ' "$MOCK_LOG"
grep -q 'updated fixture source' "$root/build/source/cage-0.3.0/cage.c" || fail 'changed commit was not staged'

# A compile failure propagates and prevents installation.
root="$tmp/failure"
make_fixture "$root"
export MOCK_ROOT="$root"
: > "$MOCK_LOG"
if PATH="$root/bin:$PATH" MESON_FAIL=compile "$root/build.sh" >"$tmp/out" 2>&1; then
    fail 'compile failure unexpectedly succeeded'
fi
grep -q '^meson compile ' "$MOCK_LOG"
if grep -q '^meson install ' "$MOCK_LOG"; then fail 'compile failure did not stop installation'; fi

# A successful build stages only the application, including on repeat builds.
root="$tmp/success with spaces"
make_fixture "$root"
export MOCK_ROOT="$root"
mkdir -p "$root/build/installed/usr/local/"{bin,lib}
touch "$root/build/installed/usr/local/bin/"{cage_xtmapper,cage_xtmapper.sh,wayland-scanner}
: > "$MOCK_LOG"
PATH="$root/bin:$PATH" "$root/build.sh" >/dev/null
grep -q '^meson compile ' "$MOCK_LOG"
if grep -q '^meson install ' "$MOCK_LOG"; then fail 'Meson development tools were installed'; fi
grep -q '^ninja -C .* -t commands cage_xtmapper' "$MOCK_LOG" || fail 'ninja link command list was not queried'
[[ -x "$root/build/installed/usr/local/bin/cage-xtmapper" ]] || fail 'single ELF was not staged into the install tree'
[[ $(find "$root/build/installed" -type f | wc -l) == 1 ]] || fail 'unexpected files remained in the install tree'
if [[ -e "$root/build/installed/usr/local/lib" ]]; then fail 'installed library dir should have been removed'; fi
[[ -x "$root/build/go/cage-xtmapper" ]] || fail 'Go ELF artifact is missing from the build tree'
grep -q '^go build -a -o .* ./cmd/cage-xtmapper$' "$MOCK_LOG" || fail 'go build did not rebuild the native link'
grep -q "$root/build/meson/libcage_xtmapper.a" "$root/build/native/pkgconfig/cage-xtmapper-native.pc" || fail 'pc contract lost the Cage library'
grep -q -- '-Wl,--start-group' "$root/build/native/pkgconfig/cage-xtmapper-native.pc" || fail 'pc contract lost the archive group'
grep -q -- '-Wl,--end-group' "$root/build/native/pkgconfig/cage-xtmapper-native.pc" || fail 'pc contract lost the archive group end'
grep -q "$root/build/meson/subprojects/wlroots/libwlroots-0.20.a" "$root/build/native/pkgconfig/cage-xtmapper-native.pc" || fail 'pc contract did not absolutize subproject archives'
grep -q -- '-I'"$root/build/source/cage-0.3.0" "$root/build/native/pkgconfig/cage-xtmapper-native.pc" || fail 'pc contract lost the Cage source include path'
grep -q 'build_type=release' "$root/build/go/provenance.txt" || fail 'provenance did not record the build type'
grep -q 'fingerprint=' "$root/build/go/provenance.txt" || fail 'provenance did not record the input fingerprint'
grep -q '^release record-build$' "$MOCK_LOG" || fail 'release metadata was not recorded'

# Failed Go compilation leaves the last successful staged application intact.
printf 'previous build\n' > "$root/build/installed/usr/local/bin/cage-xtmapper"
if PATH="$root/bin:$PATH" GO_FAIL=build "$root/build.sh" >"$tmp/out" 2>&1; then
    fail 'Go build failure unexpectedly succeeded'
fi
grep -q 'building the cage-xtmapper ELF failed' "$tmp/out"
grep -q '^previous build$' "$root/build/installed/usr/local/bin/cage-xtmapper" || fail 'failed Go build replaced the staged application'

# An uninitialized submodule checkout is reported before invoking Meson.
root="$tmp/uninitialized"
make_fixture "$root"
rm -rf -- "$root/forks/wlroots-xtmapper/.git"
export MOCK_ROOT="$root"
: > "$MOCK_LOG"
if PATH="$root/bin:$PATH" "$root/build.sh" --dry-run >"$tmp/out" 2>&1; then
    fail 'uninitialized wlroots checkout unexpectedly succeeded'
fi
grep -q 'submodule is not initialized' "$tmp/out"
[[ ! -s "$MOCK_LOG" ]] || fail 'Meson ran with an uninitialized submodule'

echo 'build.sh tests passed'
