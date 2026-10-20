package api

// The kernel, rather than mv's check-then-rename fallback, enforces no replace.
// Images need only ordinary POSIX/BusyBox tools to stage this libc-free helper.
// It is opened and unlinked before exec, so no installed utility remains.
const atomicRenameScript = `
# ocular-atomic-rename: no fallback to a potentially overwriting rename(2).
case "$(uname -m)" in
 x86_64|amd64) image='` + renameHelperAMD64 + `' ;;
 aarch64|arm64) image='` + renameHelperARM64 + `' ;;
 *) echo 'Atomic rename requires a Linux amd64 or arm64 kernel' >&2; exit 1 ;;
esac
helper=$(mktemp)
trap '[ -z "$helper" ] || rm -f "$helper"' EXIT HUP INT TERM
printf '%s' "$image" | base64 -d > "$helper"
chmod 700 "$helper"
exec 3<"$helper"
rm -f "$helper"
helper=''
if /proc/self/fd/3 "$1" "$3"; then result=0; else result=$?; fi
exec 3<&-
case "$result" in
 0) ;;
 17) echo 'Destination already exists; nothing overwritten' >&2; exit 1 ;;
 *) printf 'Atomic rename failed (status %s); no overwrite fallback was attempted\n' "$result" >&2; exit 1 ;;
esac`
