#!/usr/bin/env bash
set -Eeuo pipefail

readonly DEFAULT_REPOSITORY="SoufianoDev/cage-xtmapper"
repository="${XTMAPPER_INSTALL_REPO:-$DEFAULT_REPOSITORY}"
requested_version="${XTMAPPER_INSTALL_VERSION:-}"
installer_override="${XTMAPPER_INSTALLER_BIN:-}"
archive_override=""
# With no explicit operation, inspect the installation and act automatically.
operation=auto
test_prefix_requested=false
archive_seen=false
downloaded_archive=""

usage() {
    cat <<'EOF'
Usage: install.sh [auto|install|update|status|recover|uninstall] [options]

No operation defaults to auto: install if absent, update if stale, report when
already current, and recover a recorded incomplete transaction when possible.
Explicit operations remain available for diagnostics and maintenance.

Options:
  --version TAG       Select a published release tag instead of the latest stable release.
  --repo OWNER/REPO   Select another GitHub repository.
  --archive FILE      Use a local release archive (developer/testing option).
  --test-prefix DIR   Use an isolated test prefix; intended for developers.
  -h, --help          Show this help.

For installation/update/uninstall/recovery, run as root (for example with sudo).
The installer helper is downloaded as a prebuilt release asset; Go is not required.
EOF
}

if (($#)); then
    case "$1" in
        auto|install|update|status|recover|uninstall) operation="$1"; shift ;;
        -h|--help) usage; exit 0 ;;
        --*) ;; # Options are parsed in the next pass.
        *) printf 'install.sh: unknown operation: %s\n' "$1" >&2; usage >&2; exit 2 ;;
    esac
fi

forward_args=()
while (($#)); do
    case "$1" in
        --repo)
            (($# >= 2)) || { echo 'install.sh: --repo requires OWNER/REPO' >&2; exit 2; }
            repository="$2"
            shift 2
            ;;
        --version)
            (($# >= 2)) || { echo 'install.sh: --version requires TAG' >&2; exit 2; }
            requested_version="$2"
            shift 2
            ;;
        --archive)
            (($# >= 2)) || { echo 'install.sh: --archive requires FILE' >&2; exit 2; }
            [[ "$archive_seen" == false ]] || { echo 'install.sh: --archive may be specified only once' >&2; exit 2; }
            archive_seen=true
            archive_override="$2"
            forward_args+=(--archive "$2")
            shift 2
            ;;
        --test-prefix)
            (($# >= 2)) || { echo 'install.sh: --test-prefix requires DIR' >&2; exit 2; }
            test_prefix_requested=true
            forward_args+=(--test-prefix "$2")
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            forward_args+=("$1")
            shift
            ;;
    esac
done

[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ && "$repository" != *".."* ]] || {
    echo 'install.sh: repository must be OWNER/REPO' >&2
    exit 2
}
if [[ -n "$requested_version" && ! "$requested_version" =~ ^v[0-9][A-Za-z0-9._-]*$ ]]; then
    echo 'install.sh: invalid release tag; expected a tag beginning with v' >&2
    exit 2
fi
if [[ -n "$requested_version" && "$operation" != auto && "$operation" != install && "$operation" != update ]]; then
    echo 'install.sh: --version is valid only with automatic mode, install, or update' >&2
    exit 2
fi
if [[ -n "$requested_version" && "$archive_seen" == true ]]; then
    echo 'install.sh: do not combine --version with a local --archive' >&2
    exit 2
fi
if [[ "$archive_seen" == true && "$operation" != auto && "$operation" != install && "$operation" != update ]]; then
    echo 'install.sh: --archive is valid only with automatic mode, install, or update' >&2
    exit 2
fi
if [[ "$archive_seen" == true && ! -f "$archive_override" ]]; then
    printf 'install.sh: archive does not exist or is not a regular file: %s\n' "$archive_override" >&2
    exit 1
fi
if [[ "$operation" != status && "$test_prefix_requested" == false && "$EUID" -ne 0 ]]; then
    echo "install.sh: $operation requires root; run with sudo" >&2
    exit 1
fi
command -v mktemp >/dev/null 2>&1 || { echo 'install.sh: mktemp is required' >&2; exit 1; }
temporary="$(mktemp -d)"
trap 'rm -rf -- "$temporary"' EXIT

installer="$installer_override"
if [[ -n "$installer" ]]; then
    [[ -x "$installer" ]] || { printf 'install.sh: XTMAPPER_INSTALLER_BIN is not executable: %s\n' "$installer" >&2; exit 1; }
else
    command -v curl >/dev/null 2>&1 || { echo 'install.sh: curl is required to download releases' >&2; exit 1; }
    command -v sha256sum >/dev/null 2>&1 || { echo 'install.sh: sha256sum is required to verify downloaded assets' >&2; exit 1; }
    machine="$(uname -m)"
    case "$machine" in
        x86_64|amd64) architecture=amd64 ;;
        aarch64|arm64) architecture=arm64 ;;
        *) printf 'install.sh: unsupported CPU architecture: %s\n' "$machine" >&2; exit 1 ;;
    esac

    asset="cage-xtmapper-installer-linux-$architecture"
    repository_url="https://github.com/$repository"
    helper_release_tag=""
    effective_url=""

    if effective_url="$(curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 300 \
        --output "$temporary/$asset" --write-out '%{url_effective}' \
        "$repository_url/releases/latest/download/$asset")"; then
        if [[ "$effective_url" == "$repository_url/releases/download/"* ]]; then
            helper_release_tag="$(printf '%s\n' "$effective_url" | sed -n 's#^.*/releases/download/\([^/][^/]*\)/[^/]*$#\1#p')"
            if [[ ! "$helper_release_tag" =~ ^v[0-9][A-Za-z0-9._-]*$ ]]; then
                helper_release_tag=""
            fi
        fi
    fi

    if [[ -n "$helper_release_tag" ]]; then
        helper_url="$repository_url/releases/download/$helper_release_tag"
    elif [[ -n "$requested_version" ]]; then
        # A pinned app release may predate the latest-release helper asset.
        helper_release_tag="$requested_version"
        helper_url="$repository_url/releases/download/$helper_release_tag"
        curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 300 \
            --output "$temporary/$asset" "$helper_url/$asset"
    else
        echo 'install.sh: could not find a published release installer asset' >&2
        exit 1
    fi

    curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 60 \
        --output "$temporary/$asset.sha256" "$helper_url/$asset.sha256"
    (cd "$temporary" && sha256sum --check "$asset.sha256")
    chmod 0755 "$temporary/$asset"
    installer="$temporary/$asset"

    if [[ "$operation" == auto || "$operation" == install || "$operation" == update ]] && [[ "$archive_seen" == false ]]; then
        archive_tag="${requested_version:-$helper_release_tag}"
        archive_url="$repository_url/releases/download/$archive_tag"
        archive_name="cage-xtmapper-$archive_tag-$architecture.tar"
        curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 600 \
            --output "$temporary/$archive_name" "$archive_url/$archive_name"
        curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 60 \
            --output "$temporary/$archive_name.sha256" "$archive_url/$archive_name.sha256"
        (cd "$temporary" && sha256sum --check "$archive_name.sha256")
        downloaded_archive="$archive_name"
    fi
fi

if [[ "$operation" == auto || "$operation" == install || "$operation" == update ]] && [[ "$archive_seen" == false && -n "$installer_override" ]]; then
    if [[ -z "$requested_version" ]]; then
        echo 'install.sh: with XTMAPPER_INSTALLER_BIN, provide --archive or --version' >&2
        exit 2
    fi
    command -v curl >/dev/null 2>&1 || { echo 'install.sh: curl is required to download the selected archive' >&2; exit 1; }
    command -v sha256sum >/dev/null 2>&1 || { echo 'install.sh: sha256sum is required to verify the selected archive' >&2; exit 1; }
    machine="$(uname -m)"
    case "$machine" in
        x86_64|amd64) architecture=amd64 ;;
        aarch64|arm64) architecture=arm64 ;;
        *) printf 'install.sh: unsupported CPU architecture: %s\n' "$machine" >&2; exit 1 ;;
    esac
    repository_url="https://github.com/$repository"
    release_url="$repository_url/releases/download/$requested_version"
    archive_name="cage-xtmapper-$requested_version-$architecture.tar"
    curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 600 \
        "$release_url/$archive_name" --output "$temporary/$archive_name"
    curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 60 \
        "$release_url/$archive_name.sha256" --output "$temporary/$archive_name.sha256"
    (cd "$temporary" && sha256sum --check "$archive_name.sha256")
    downloaded_archive="$archive_name"
fi

if [[ "$operation" != status ]]; then
    printf 'Cage XtMapper: operation=%s repository=%s' "$operation" "$repository"
    [[ -z "$requested_version" ]] || printf ' version=%s' "$requested_version"
    printf '\n'
fi
if [[ -n "$downloaded_archive" ]]; then
    exec {archive_fd}< "$temporary/$downloaded_archive"
    forward_args+=(--archive "/proc/self/fd/$archive_fd")
fi
exec {installer_fd}< "$installer"
# Exec the unlinked inodes to preserve exit statuses/signals and the same pid;
# a forked child would keep bash's cleanup trap racing files the helper reads.
rm -rf -- "$temporary"
trap - EXIT
exec "/proc/self/fd/$installer_fd" "$operation" "${forward_args[@]}"
