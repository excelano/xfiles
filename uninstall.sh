#!/bin/sh
# xfiles uninstaller — finds and removes the xftp, xcp, xfind, xtree, and xsync binaries,
# with an optional follow-up step to remove their config dirs under ~/.config
# (REPL history, cached tokens). POSIX sh, no bash extensions.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/excelano/xfiles/main/uninstall.sh | sh
#
# Environment variables:
#   XFILES_UNINSTALL_YES=1  Skip the binary-removal confirmation (assume yes).
#                        Does NOT imply purge: config dirs are kept
#                        unless XFILES_PURGE=1 is also set.
#   XFILES_PURGE=1          Also remove the ~/.config/{xftp,xcp,xfind,xtree,xsync}
#                        config dirs (history, cached tokens), independent of
#                        XFILES_UNINSTALL_YES.

set -eu

BINARIES="xftp xcp xfind xtree xsync"

say() { printf '%s\n' "$*" >&2; }
err() { say "error: $*"; exit 1; }

# read_yes reads a y/N answer from the controlling terminal, not stdin,
# because this script is typically invoked as `curl ... | sh` where stdin
# is the script itself.
read_yes() {
	prompt="$1"
	if [ "${XFILES_UNINSTALL_YES:-0}" = "1" ]; then
		return 0
	fi
	if [ ! -t 0 ] && [ ! -e /dev/tty ]; then
		err "no terminal available for confirmation; re-run with XFILES_UNINSTALL_YES=1 to skip the prompt"
	fi
	printf '%s [y/N]: ' "$prompt" >&2
	if [ -e /dev/tty ]; then
		read ans </dev/tty
	else
		read ans
	fi
	case "$ans" in
		y|Y|yes|YES) return 0 ;;
		*) return 1 ;;
	esac
}

# remove_binary finds and removes one binary by name, warning about any
# additional copies left elsewhere on PATH.
remove_binary() {
	BIN="$1"
	if ! command -v "$BIN" >/dev/null 2>&1; then
		say "$BIN is not on PATH; skipping."
		return 0
	fi
	TARGET=$(command -v "$BIN")
	say "Found $BIN at $TARGET"
	if [ ! -w "$TARGET" ] && [ ! -w "$(dirname "$TARGET")" ]; then
		err "$TARGET is not writable; re-run with sudo to remove it"
	fi
	if ! read_yes "Remove $TARGET?"; then
		say "Skipped $TARGET"
		return 0
	fi
	rm -f "$TARGET" || err "could not remove $TARGET"
	say "Removed $TARGET"

	# Invalidate the shell's command hash; without this, `command -v` happily
	# reports the just-deleted path as still present and the duplicate-install
	# check below cries wolf.
	hash -r 2>/dev/null || true

	LEFTOVER=$(command -v "$BIN" 2>/dev/null || true)
	if [ -n "$LEFTOVER" ]; then
		say "Note: another $BIN binary is still on PATH at $LEFTOVER"
		say "Re-run this uninstaller to remove it, or remove it manually."
	fi
}

# remove_config handles the optional state cleanup for one config dir,
# decoupled from the binary confirmation on purpose: XFILES_UNINSTALL_YES means
# "don't ask me about the binary", NOT "delete my data". Purging is opt-in via
# XFILES_PURGE=1 or an explicit interactive yes.
remove_config() {
	DIR="${XDG_CONFIG_HOME:-$HOME/.config}/$1"
	[ -d "$DIR" ] || return 0
	if [ "${XFILES_PURGE:-0}" = "1" ]; then
		rm -rf "$DIR"
		say "Removed $DIR"
	elif [ "${XFILES_UNINSTALL_YES:-0}" = "1" ]; then
		say "Kept $DIR; set XFILES_PURGE=1 to remove it"
	elif read_yes "Also remove $DIR?"; then
		rm -rf "$DIR"
		say "Removed $DIR"
	else
		say "Kept $DIR"
	fi
}

ANY=0
for BIN in $BINARIES; do
	if command -v "$BIN" >/dev/null 2>&1; then
		ANY=1
	fi
done
if [ "$ANY" = "0" ]; then
	say "None of xftp, xcp, xfind, xtree, or xsync is on PATH; nothing to uninstall."
	say "If you installed to a custom location, remove it manually."
	exit 0
fi

for BIN in $BINARIES; do
	remove_binary "$BIN"
done

for BIN in $BINARIES; do
	remove_config "$BIN"
done

say ""
say "Done."
