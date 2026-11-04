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
    var/lib/sandboxd/credential_encryption.key|var/lib/sandboxd/credential_encryption.key.ring|var/lib/sandboxd/credential_encryption.key.wrapped)
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
mkdir -p "$target_root"
# A restore replaces the node's database and Raft log, never merges into
# them: SQLite would replay a leftover state.db-wal from the database being
# replaced onto the restored one, and two Raft logs in one directory are not
# one log. A replacement node that already ran a fresh install has both.
# What the archive brings is moved aside first, kept rather than deleted.
listing="$(tar -tzf "$input")"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
set_aside() {
  local path="$target_root/$1"
  if [[ -e "$path" ]]; then
    mv "$path" "$path.pre-restore-$stamp"
    echo "moved aside: $path -> $path.pre-restore-$stamp"
  fi
}
if grep -qx '\./var/lib/sandboxd/state\.db' <<<"$listing"; then
  set_aside var/lib/sandboxd/state.db
  set_aside var/lib/sandboxd/state.db-wal
  set_aside var/lib/sandboxd/state.db-shm
fi
if grep -qE '^\./var/lib/sandboxd/raft/?$' <<<"$listing"; then
  set_aside var/lib/sandboxd/raft
fi
# The server key goes with the database it seals: the node must come back
# with the archive's key files and none of the replaced node's. sandboxd
# refuses wrapped custody beside a plaintext key or ring, and a fresh
# install's own key, ring or wrapped file would otherwise sit beside the
# restored ones. The archive's own copies replace the node's (kept aside
# for rollback); a node file the archive's custody has no use for is moved
# aside after extracting.
key_dir=var/lib/sandboxd
in_archive() { grep -qx "\./$key_dir/$1" <<<"$listing"; }
for k in credential_encryption.key credential_encryption.key.ring credential_encryption.key.wrapped; do
  in_archive "$k" && set_aside "$key_dir/$k"
done
# --- Staged extraction, then move into place ----------------------------------
# Extraction stages into a private temp dir with --no-same-owner
# --no-same-permissions and only the allowlisted trees are moved into
# place; set_aside above already cleared (among others) the SQLite WAL/SHM
# sidecars and the key files the archive does not carry.
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
# The server key files the archive carries (upstream's backup emits them;
# set_aside above cleared the node's own). Absent from the archive, each
# move is a no-op — the tail's custody handling decides what stays.
move_into_place "var/lib/sandboxd/credential_encryption.key"
move_into_place "var/lib/sandboxd/credential_encryption.key.ring"
move_into_place "var/lib/sandboxd/credential_encryption.key.wrapped"
move_into_place "etc/sandboxd"
echo "restored $input into $target_root"
if in_archive credential_encryption.key.wrapped; then
  set_aside "$key_dir/credential_encryption.key"
  set_aside "$key_dir/credential_encryption.key.ring"
else
  set_aside "$key_dir/credential_encryption.key.wrapped"
  in_archive credential_encryption.key.ring || set_aside "$key_dir/credential_encryption.key.ring"
  if ! in_archive credential_encryption.key && [[ -e "$target_root/$key_dir/credential_encryption.key" ]]; then
    if in_archive credential_encryption.key.ring ||
      grep -qsE '^[[:space:]]*SB_CREDENTIAL_ENCRYPTION_KEY[[:space:]]*=[[:space:]]*[^[:space:]]' \
        "$target_root/etc/sandboxd/sandboxd.env" "$target_root/etc/sandboxd/cluster.env"; then
      # The archive's ring or env key is the server key; this one is not.
      set_aside "$key_dir/credential_encryption.key"
    else
      # An archive made before backups carried the key file: the node's
      # own key stays, and opens the restored data only if it is the
      # backed-up node's key (a restore in place).
      echo "WARNING: the archive carries no server key; sandboxd will use $target_root/$key_dir/credential_encryption.key, which must be the backed-up node's own key" >&2
    fi
  fi
fi
wrapped="$target_root/var/lib/sandboxd/credential_encryption.key.wrapped"
if [[ -f "$wrapped" ]]; then
  # Key custody "wrapped": sandboxd unwraps this at boot through the key
  # store, which must be reachable. A backup taken before a key version was
  # retired no longer opens once that version is gone.
  chmod 0600 "$wrapped"
  echo "the server key is wrapped: run \`sandboxd key verify\` with the key store reachable before starting sandboxd"
fi
ring="$target_root/var/lib/sandboxd/credential_encryption.key.ring"
if [[ -f "$ring" ]]; then
  # A rotated plaintext key (custody "file"): the ring holds every live key.
  chmod 0600 "$ring"
  echo "the server key was rotated: the ring at $ring holds it; keep it with SB_CREDENTIAL_KEY_RING_PATH unset or pointing there"
fi
echo "verify ownership/modes, then start sandboxd"
