#!/usr/bin/env bash
# manygit installer. Usage:
#   curl -fsSL https://raw.githubusercontent.com/rabeeh-ta/manygit/main/install.sh | bash
#
# Installs the latest release by default. To pin a version (e.g. to roll back),
# pass it as an argument or in MANYGIT_VERSION:
#   curl -fsSL .../install.sh | bash -s -- v1.0.7
#   MANYGIT_VERSION=v1.0.7 ./install.sh
set -euo pipefail

repo="rabeeh-ta/manygit"
bin="manygit"
install_dir="${MANYGIT_INSTALL_DIR:-$HOME/.local/bin}"

die() { printf 'error: %s\n' "$1" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar  >/dev/null 2>&1 || die "tar is required"

os=$(uname -s)
case "$os" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS: $os (manygit supports Linux and macOS)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

# An explicit version (arg or MANYGIT_VERSION) pins the install; otherwise the
# newest release wins. Pinning is how you roll back to an earlier build.
tag="${MANYGIT_VERSION:-${1:-}}"
pinned=""
if [ -n "$tag" ]; then
  pinned=yes
  case "$tag" in v*) ;; *) tag="v$tag" ;; esac   # accept "1.0.7" or "v1.0.7"
  curl -fsSL -o /dev/null "https://api.github.com/repos/$repo/releases/tags/$tag" \
    || die "no release tagged $tag (see https://github.com/$repo/releases)"
else
  tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" \
        | grep '"tag_name"' | head -1 | cut -d'"' -f4)
  [ -n "$tag" ] || die "no published release found for $repo yet"
fi

asset="${bin}_${os}_${arch}.tar.gz"
url="https://github.com/$repo/releases/download/$tag/$asset"

printf 'Installing %s %s (%s/%s)...\n' "$bin" "$tag" "$os" "$arch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp/$asset" || die "download failed: $url"
tar -xzf "$tmp/$asset" -C "$tmp"   || die "could not extract $asset"
[ -f "$tmp/$bin" ] || die "archive did not contain $bin"

mkdir -p "$install_dir"
mv "$tmp/$bin" "$install_dir/$bin"
chmod +x "$install_dir/$bin"
printf 'Installed to %s\n' "$install_dir/$bin"

# A pinned version is usually a rollback, and manygit's launch check would offer
# to pull it straight back to newest. Say so instead of letting it surprise them.
if [ -n "$pinned" ]; then
  printf 'Pinned to %s. manygit checks for a newer release on launch — answer "n",\nor use --no-update-check / MANYGIT_NO_UPDATE_CHECK=1 to stay on this version.\n' "$tag"
fi

# Put install_dir on PATH if it isn't already.
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    case "${SHELL:-}" in
      */zsh) rc="$HOME/.zshrc" ;;
      *)     rc="$HOME/.bashrc" ;;
    esac
    printf '\n# manygit\nexport PATH="%s:$PATH"\n' "$install_dir" >> "$rc"
    printf 'Added %s to your PATH in %s. Restart your shell (or open a new tab) to pick it up.\n' "$install_dir" "$rc"
    ;;
esac

printf 'Done. Run: %s\n' "$bin"
