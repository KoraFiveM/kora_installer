#!/bin/sh
# Install the GitHub Actions-built CLI without modifying any server directories.
set -eu
case "$(uname -s)/$(uname -m)" in Linux/x86_64|Linux/amd64) asset=kora-linux-amd64;; *) echo 'This script supports Linux/WSL x86-64. Download kora-windows-amd64.exe for native Windows.' >&2; exit 1;; esac
repo=KoraFiveM/kora_installer
version=${KORA_VERSION:-latest}
case "$version" in *[!A-Za-z0-9._-]*|'') echo 'Invalid KORA_VERSION' >&2; exit 1;; esac
base="https://github.com/$repo/releases/latest/download"
if [ "$version" != latest ]; then base="https://github.com/$repo/releases/download/$version"; fi
bin_dir=${KORA_BIN_DIR:-"$HOME/.local/bin"}
mkdir -p "$bin_dir"
if [ -e "$bin_dir/kora" ] || [ -L "$bin_dir/kora" ]; then echo "Already installed: $bin_dir/kora. Run it directly; this installer never overwrites an existing binary." >&2; exit 1; fi
work=$(mktemp -d "${TMPDIR:-/tmp}/kora-install.XXXXXXXX")
staged=''
trap 'rm -rf "$work"; if [ -n "$staged" ]; then rm -f "$staged"; fi' EXIT HUP INT TERM
fetch() {
 if command -v curl >/dev/null 2>&1; then curl -fSL --proto '=https' --tlsv1.2 "$1" -o "$2"
 elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
 else echo 'Install curl or wget first.' >&2; exit 1; fi
}
if ! fetch "$base/$asset" "$work/$asset"; then
 echo "Unable to download $asset. Verify a kora-v* release has been published at https://github.com/$repo/releases." >&2
 exit 1
fi
fetch "$base/$asset.sha256" "$work/checksum"
expected=$(awk 'NR==1 {print $1}' "$work/checksum")
case "$expected" in *[!a-fA-F0-9]*|'') echo 'Invalid checksum file' >&2; exit 1;; esac
[ "${#expected}" -eq 64 ] || { echo 'Invalid checksum length' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$work/$asset" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$work/$asset" | awk '{print $1}')
else echo 'Install sha256sum or shasum first.' >&2; exit 1; fi
[ "$actual" = "$expected" ] || { echo 'Checksum mismatch; binary was not installed.' >&2; exit 1; }
staged=$(mktemp "$bin_dir/.kora.XXXXXXXX")
cat "$work/$asset" > "$staged"
chmod 755 "$staged"
# Exclusive hard-link creation prevents replacement even if another installer races us.
ln "$staged" "$bin_dir/kora"
rm -f "$staged"; staged=''
printf 'Installed %s/kora\n' "$bin_dir"
case ":$PATH:" in *":$bin_dir:"*) ;; *) printf 'Add this directory to PATH: %s\n' "$bin_dir";; esac
if [ "${1:-}" = --install-only ]; then exit 0; fi
unattended=false
for argument in "$@"; do case "$argument" in --yes|--yes=true|-yes|-yes=true) unattended=true;; esac; done
if [ "$unattended" = true ]; then "$bin_dir/kora" "$@"
elif [ -r /dev/tty ] && ( : </dev/tty ) 2>/dev/null; then "$bin_dir/kora" "$@" </dev/tty
else printf 'Open a terminal and run: %s/kora\n' "$bin_dir"; fi
