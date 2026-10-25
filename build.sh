#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
    cat <<'USAGE'
Usage: build.sh [--dry-run] [--debug]

  --debug    Build Cage and wlroots in debug mode (default: release).
  --dry-run  Prepare sources and configure Meson, but skip compile and install.
USAGE
}

dry_run=false
build_type=release
while (($#)); do
    case "$1" in
        --dry-run)
            if [[ "$dry_run" == true ]]; then
                echo "--dry-run may only be specified once" >&2
                usage >&2
                exit 2
            fi
            dry_run=true
            ;;
        --debug)
            if [[ "$build_type" == debug ]]; then
                echo "--debug may only be specified once" >&2
                usage >&2
                exit 2
            fi
            build_type=debug
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
    shift
done

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
build_root="$script_dir/build"
source_root="$build_root/source"
source_dir="$source_root/cage-0.3.0"
meson_build_dir="$build_root/meson"
prepared_marker='.cage-xtmapper-prepared'
cage_source="$script_dir/native-src/cage"
wlroots_source="$script_dir/native-src/wlroots-xtmapper"

fail() {
    echo "build.sh: $*" >&2
    exit 1
}

require_native_tree() {
    local name="$1" path="$2"
    [[ -d "$path" ]] || fail "$name source is missing at $path; a normal checkout of this repository provides it under native-src/"
    [[ ! -e "$path/.git" ]] || fail "$name source at $path must not contain Git metadata"
}

require_native_tree Cage "$cage_source"
require_native_tree wlroots "$wlroots_source"
command -v clang >/dev/null 2>&1 || fail "Clang (clang) is required to build the native code"
export CC=clang
command -v go >/dev/null 2>&1 || fail "the Go toolchain (go) is required to build cage-xtmapper"

shopt -s nullglob
wrapfiles=("$script_dir"/meson_wrapfiles/*)
dependencies=("$script_dir"/wlroots_deps/*)
((${#wrapfiles[@]} > 0)) || fail "No Meson wrap files found"
((${#dependencies[@]} > 0)) || fail "No wlroots dependency archives found"

input_fingerprint() {
    (cd -- "$script_dir" && go run ./tools/release input-fingerprint --build-type "$build_type")
}

stage_tree() {
    local source="$1" destination="$2" description="$3"
    echo "Assembling $description from native-src"
    mkdir -p -- "$destination"
    cp -a -- "$source/." "$destination/" || fail "Failed to assemble $description from $source"
}

mkdir -p -- "$build_root"
current_fingerprint="$(input_fingerprint)" || fail "computing the build-input fingerprint failed"
if [[ -d "$source_dir" && -f "$source_dir/$prepared_marker" ]]; then
    prepared_fingerprint="$(<"$source_dir/$prepared_marker")"
else
    prepared_fingerprint=''
fi

if [[ "$prepared_fingerprint" != "$current_fingerprint" ]]; then
    rm -rf -- "$source_dir"
    rm -rf -- "$meson_build_dir"
    mkdir -p -- "$source_dir/subprojects/wlroots/subprojects/packagefiles"
    stage_tree "$cage_source" "$source_dir" Cage
    stage_tree "$wlroots_source" "$source_dir/subprojects/wlroots" wlroots
    cp -- "${wrapfiles[@]}" "$source_dir/subprojects/wlroots/subprojects/"
    ln -sf -- "${dependencies[@]}" "$source_dir/subprojects/wlroots/subprojects/packagefiles/"
    printf '%s\n' "$current_fingerprint" > "$source_dir/$prepared_marker"
elif [[ ! -d "$source_dir/subprojects/wlroots/subprojects/packagefiles" ]]; then
    fail "Prepared source tree is incomplete at $source_dir; remove it and rerun build.sh."
fi

# Set the root and scoped wlroots options together, including when switching
# modes in an existing Meson configuration.
meson_setup_args=(
    "--buildtype=$build_type"
    -Ddefault_library=static
    -Dprefix=/usr/local
    "-Dwlroots:buildtype=$build_type"
    "-Dforce_fallback_for=wayland-server,wayland-client,xkbcommon,pixman-1"

    -Dwayland:werror=false
    -Dlibdrm:werror=false
    -Dlibxkbcommon:werror=false
)
if [[ -f "$meson_build_dir/meson-private/coredata.dat" ]]; then
    meson setup --reconfigure "$meson_build_dir" "$source_dir" "${meson_setup_args[@]}"
else
    meson setup "$meson_build_dir" "$source_dir" "${meson_setup_args[@]}"
fi

if [[ "$dry_run" == true ]]; then
    echo "Dry run: skipped compile and install"
    exit 0
fi

if ! command -v ninja >/dev/null; then
    fail "ninja is required to derive the Go link contract"
fi

meson compile -C "$meson_build_dir"

# Reuse the standalone target's dependency graph, replacing its objects with the Cage library.
mkdir -p -- "$build_root/go" "$build_root/native/pkgconfig"
if ! link_line="$(ninja -C "$meson_build_dir" -t commands cage_xtmapper | tail -n 1)"; then
    fail "could not query the cage_xtmapper link command"
fi
[[ -n "$link_line" ]] || fail "Meson did not produce a cage_xtmapper link command"
pc_file="$build_root/native/pkgconfig/cage-xtmapper-native.pc"
(cd -- "$script_dir" && go run ./tools/release link-contract "$meson_build_dir" "$source_dir" "$link_line") > "$pc_file" || fail "generating the native link contract failed"

# Go's cache does not track changes to the external native archives.
if ! (cd "$script_dir" && CGO_ENABLED=1 PKG_CONFIG_PATH="$build_root/native/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}" go build -a -o "$build_root/go/cage-xtmapper" ./cmd/cage-xtmapper); then
    fail "building the cage-xtmapper ELF failed"
fi
printf 'build_type=%s\nfingerprint=%s\n%s\n' \
    "$build_type" \
    "$current_fingerprint" \
    "$(go version)" > "$build_root/go/provenance.txt"
(cd -- "$script_dir" && go run ./tools/release record-build) || fail "recording release build metadata failed"

rm -rf -- "$build_root/installed"
installed_bin="$build_root/installed/usr/local/bin"
mkdir -p -- "$installed_bin"
install -m755 -- "$build_root/go/cage-xtmapper" "$installed_bin/cage-xtmapper"
