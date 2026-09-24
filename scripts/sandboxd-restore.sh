#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: sandboxd-restore.sh --input /path/sandboxd-backup.tar.gz --target-root / [--force]

Restores the archive produced by sandboxd-backup.sh. Stop sandboxd first.

Every archive member is validated against the exact set sandboxd-backup.sh
emits (MANIFEST.txt, var/lib/sandboxd/state.db, var/lib/sandboxd/raft/**,
etc/sandboxd/**) BEFORE extraction. Absolute paths, ".." traversal, symlink/
hardlink/special members, and setuid/setgid/sticky members are rejected.
Extraction stages into a private temp dir with --no-same-owner
--no-same-permissions and only the allowlisted trees are moved into place.

Options:
  --input PATH        Backup archive. Required.
  --target-root PATH  Restore root. Default: /
  --force            Required acknowledgement for writes.
  -h, --help         Show this help.
USAGE
}

die() {
  echo "sandboxd-restore: $*" >&2
  exit 1
}

input=""
target_root="/"
force="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --input) input="${2:?missing --input value}"; shift 2 ;;
    --target-root) target_root="${2:?missing --target-root value}"; shift 2 ;;
    --force) force="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ -z "$input" ]]; then
  echo "--input is required" >&2
  usage >&2
  exit 2
fi
if [[ "$force" != "true" ]]; then
  echo "refusing to restore without --force; stop sandboxd and confirm this is the intended target" >&2
  exit 2
fi
if [[ ! -f "$input" ]]; then
  echo "backup archive not found: $input" >&2
  exit 1
fi

# Allowlist of exactly what sandboxd-backup.sh emits (see copy_if_exists there):
# MANIFEST.txt, the state DB at var/lib/sandboxd/state.db, the raft dir at
# var/lib/sandboxd/raft/ and the config dir at etc/sandboxd/ (both recursive).
member_allowed() {
  local name="$1"
  case "$name" in
    ""|MANIFEST.txt|var|var/lib|var/lib/sandboxd|var/lib/sandboxd/state.db|etc)
      return 0
      ;;
    var/lib/sandboxd/raft|var/lib/sandboxd/raft/*)
      return 0
      ;;
    etc/sandboxd|etc/sandboxd/*)
      return 0
      ;;
  esac
  return 1
}

# --- Listing check BEFORE any extraction (tar -t) -----------------------------
# Extraction is only safe once every member is known to be an allowlisted
# regular file/directory with no special bits. Crafted archives otherwise
# escape the target root via ../ or absolute members, or smuggle setuid
# binaries / symlink escapes into place.
names="$(tar -tzf "$input")" || die "cannot list archive (not a tar.gz?): $input"
verbose="$(tar -tvzf "$input")" || die "cannot list archive (not a tar.gz?): $input"
[[ -n "$names" ]] || die "archive is empty: $input"

while IFS= read -r member; do
  [[ -z "$member" ]] && continue
  case "$member" in
    /*) die "rejecting absolute member path: $member" ;;
  esac
  name="${member#./}"
  name="${name%/}"
  case "/$name/" in
    */../*) die "rejecting path-traversal member: $member" ;;
  esac
  member_allowed "$name" || die "member outside the sandboxd-backup.sh allowlist: $member"
done <<<"$names"

while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  mode="${line%% *}"
  case "${mode:0:1}" in
    -|d) ;;
    l) die "rejecting symlink member" ;;
    h) die "rejecting hardlink member" ;;
    *) die "rejecting special-file member (type '${mode:0:1}')" ;;
  esac
  # setuid / setgid / sticky live at mode positions 4/7/10 (0-indexed 3/6/9).
  if [[ "${mode:3:1}" == [sS] || "${mode:6:1}" == [sS] || "${mode:9:1}" == [tT] ]]; then
    die "rejecting setuid/setgid/sticky member (mode $mode)"
  fi
done <<<"$verbose"

# --- Staged extraction, then move into place ----------------------------------
staging="$(mktemp -d)"
cleanup() {
  rm -rf "$staging"
}
trap cleanup EXIT

tar -C "$staging" --no-same-owner --no-same-permissions -xzf "$input"

move_into_place() {
  local rel="$1"
  [[ -e "$staging/$rel" ]] || return 0
  local dest="${target_root%/}/$rel"
  mkdir -p "$(dirname "$dest")"
  rm -rf "$dest"
  mv "$staging/$rel" "$dest"
}

move_into_place "MANIFEST.txt"
move_into_place "var/lib/sandboxd/state.db"
move_into_place "var/lib/sandboxd/raft"
move_into_place "etc/sandboxd"

echo "restored $input into $target_root"
echo "verify ownership/modes, then start sandboxd"
