#!/usr/bin/env bash
# test-sandboxd-restore.sh — exercises sandboxd-restore.sh's archive safety.
#
# 1. A legitimate sandboxd-backup.sh archive must restore (round-trip).
# 2. Crafted malicious archives (path traversal, absolute members, symlink /
#    hardlink members, setuid members, non-allowlisted paths) must be rejected
#    BEFORE extraction.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
RESTORE="$HERE/sandboxd-restore.sh"
BACKUP="$HERE/sandboxd-backup.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

make_evil() {
  local kind="$1"
  local out="$2"
  python3 - "$kind" "$out" <<'PY'
import io
import sys
import tarfile

kind, out = sys.argv[1], sys.argv[2]
tf = tarfile.open(out, "w:gz")


def add_file(name, data=b"pwned\n", mode=0o644):
    info = tarfile.TarInfo(name)
    info.type = tarfile.REGTYPE
    info.mode = mode
    info.size = len(data)
    tf.addfile(info, io.BytesIO(data))


def add_link(name, target, linktype):
    info = tarfile.TarInfo(name)
    info.type = linktype
    info.linkname = target
    info.mode = 0o777
    tf.addfile(info)


if kind == "traversal":
    add_file("../../etc/cron.d/evil")
elif kind == "absolute":
    add_file("/etc/cron.d/evil")
elif kind == "symlink":
    add_link("etc/sandboxd/evil-link", "/etc/passwd", tarfile.SYMTYPE)
elif kind == "hardlink":
    add_file("etc/sandboxd/sandboxd.env")
    add_link("etc/sandboxd/evil-hard", "etc/sandboxd/sandboxd.env", tarfile.LNKTYPE)
elif kind == "setuid":
    add_file("etc/sandboxd/suid-bin", mode=0o4755)
elif kind == "outside-allowlist":
    add_file("etc/shadow")
elif kind == "traversal-and-setuid":
    add_file("../../etc/cron.d/evil")
    add_file("etc/sandboxd/suid-bin", mode=0o4755)
else:
    raise SystemExit("unknown kind: " + kind)
tf.close()
PY
}

failures=0

# --- Happy path: sandboxd-backup.sh round-trip -------------------------------
mkdir -p "$work/host/raft/sub" "$work/host/config/tls"
echo "statedb" > "$work/host/state.db"
echo "raftdata" > "$work/host/raft/raft.db"
echo "subdata" > "$work/host/raft/sub/nested.db"
echo "SB_PAT_TOKEN=test" > "$work/host/config/sandboxd.env"
echo "ca" > "$work/host/config/tls/ca.crt"

"$BACKUP" \
  --state-db "$work/host/state.db" \
  --raft-dir "$work/host/raft" \
  --config-dir "$work/host/config" \
  --output "$work/good.tar.gz"

mkdir -p "$work/good-target"
if ! "$RESTORE" --input "$work/good.tar.gz" --target-root "$work/good-target" --force; then
  echo "FAIL: legitimate backup rejected" >&2
  failures=$((failures + 1))
else
  ok=true
  for rel in MANIFEST.txt var/lib/sandboxd/state.db var/lib/sandboxd/raft/raft.db \
             var/lib/sandboxd/raft/sub/nested.db etc/sandboxd/sandboxd.env etc/sandboxd/tls/ca.crt; do
    if [[ ! -f "$work/good-target/$rel" ]]; then
      echo "FAIL: missing after restore: $rel" >&2
      ok=false
    fi
  done
  if [[ "$(cat "$work/good-target/var/lib/sandboxd/state.db")" != "statedb" ]]; then
    echo "FAIL: state.db content mismatch after restore" >&2
    ok=false
  fi
  if [[ "$ok" != "true" ]]; then
    failures=$((failures + 1))
  else
    echo "PASS: legitimate backup restores"
  fi
fi

# --- Malicious archives must all be rejected ---------------------------------
for kind in traversal absolute symlink hardlink setuid outside-allowlist traversal-and-setuid; do
  make_evil "$kind" "$work/evil-$kind.tar.gz"
  mkdir -p "$work/evil-target-$kind"
  marker="$work/evil-target-$kind/MARKER"
  echo "untouched" > "$marker"
  if "$RESTORE" --input "$work/evil-$kind.tar.gz" --target-root "$work/evil-target-$kind" --force; then
    echo "FAIL: malicious archive accepted: $kind" >&2
    failures=$((failures + 1))
  else
    # Nothing may have landed in the target root.
    if [[ "$(cat "$marker")" != "untouched" ]] || [[ -e "$work/evil-target-$kind/etc" ]]; then
      echo "FAIL: malicious archive rejected but still wrote into target: $kind" >&2
      failures=$((failures + 1))
    else
      echo "PASS: malicious archive rejected: $kind"
    fi
  fi
done

if [[ "$failures" -ne 0 ]]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all sandboxd-restore checks passed"
