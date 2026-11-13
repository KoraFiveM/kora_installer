#!/bin/sh
# Build the CLI from Git source without requiring a published release.
set -eu
case "$(uname -s)/$(uname -m)" in Linux/x86_64|Linux/amd64) true;; *) echo 'This script supports Linux/WSL x86-64. For native Windows, clone the repository and run go build -o kora.exe .' >&2; exit 1;; esac
repo=KoraFiveM/kora_installer
ref=${KORA_REF:-${KORA_VERSION:-main}}
[ "$ref" != latest ] || ref=main
case "$ref" in -*|*[!A-Za-z0-9._/-]*|'') echo 'Invalid KORA_REF (use a branch or tag)' >&2; exit 1;; esac
for tool in git go; do
 if ! command -v "$tool" >/dev/null 2>&1; then
  echo "Install $tool first. Git: https://git-scm.com/install/ · Go 1.26+: https://go.dev/doc/install" >&2
  exit 1
 fi
done
bin_dir=${KORA_BIN_DIR:-"$HOME/.local/bin"}
mkdir -p "$bin_dir"
if [ -e "$bin_dir/kora" ] || [ -L "$bin_dir/kora" ]; then echo "Already installed: $bin_dir/kora. Run it directly; this installer never overwrites an existing binary." >&2; exit 1; fi
work=$(mktemp -d "${TMPDIR:-/tmp}/kora-install.XXXXXXXX")
staged=''
trap 'rm -rf "$work"; if [ -n "$staged" ]; then rm -f "$staged"; fi' EXIT HUP INT TERM
printf 'Cloning %s (%s)…\n' "$repo" "$ref"
if ! git clone --depth 1 --single-branch --branch "$ref" "https://github.com/$repo.git" "$work/source"; then
 echo 'Unable to clone source. Check the branch/tag and GitHub access; private repositories need Git credentials (gh auth login && gh auth setup-git).' >&2
 exit 1
fi
printf 'Building kora from source (requires Go 1.26+; the first build may download the toolchain and modules)…\n'
if ! (cd "$work/source" && GOWORK=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -mod=readonly -trimpath -o "$work/kora" .); then
 echo 'Source build failed. Install Go 1.26+ from https://go.dev/doc/install and check network access for Go modules. Nothing was installed.' >&2
 exit 1
fi
staged=$(mktemp "$bin_dir/.kora.XXXXXXXX")
cat "$work/kora" > "$staged"
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
