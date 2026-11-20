#!/usr/bin/env bash
# Run only on an ephemeral GitHub-hosted macOS runner. No real tap or token.
set -euo pipefail
: "${RUNNER_TEMP:?}" "${GITHUB_ACTIONS:?}" "${EXPECTED_ARCH:?}" "${TAG:?}" "${ASSET_DIR:?}"
[[ "$GITHUB_ACTIONS" = true && "${RUNNER_ENVIRONMENT:-}" = github-hosted ]] || { echo 'Refusing non-ephemeral runner' >&2; exit 1; }
[[ "$(uname -m)" = "$EXPECTED_ARCH" ]] || { echo "Non-native runner: uname -m is $(uname -m)" >&2; exit 1; }
# /usr/bin/arch reports the i386 family on Intel macOS, so it is not a valid
# native-architecture probe; use sysctl instead to reject Rosetta translation.
[[ "${HOMEBREW_ARCH:-}" = "" && "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 0 ]] || { echo 'Running under translation' >&2; exit 1; }
if [[ "$EXPECTED_ARCH" = arm64 ]]; then
  [[ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" = 1 ]] || { echo 'Not an Apple Silicon host' >&2; exit 1; }
else
  [[ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" = 0 ]] || { echo 'Not an Intel host' >&2; exit 1; }
fi
case "$EXPECTED_ARCH" in arm64) goarch=arm64;; x86_64) goarch=amd64;; *) exit 1;; esac
bash scripts/acceptance/release-assets.sh verify "$ASSET_DIR" "$TAG"
version=${TAG#v}; old=28522afbc47f8abec9590dbdfe10de1503bf2b53
[[ "$(git rev-parse 'refs/tags/v2.0.2^{commit}')" = "$old" ]] || { echo 'Unexpected v2.0.2 tag' >&2; exit 1; }
cellar=$(brew --cellar); prefix=$(brew --prefix); repo=$(brew --repository)
tap="$repo/Library/Taps/puppetdevz/homebrew-tap"
for path in "$cellar/ianchor" "$prefix/bin/ianchor" "$tap"; do
  [[ ! -e "$path" && ! -L "$path" ]] || { echo "Refusing existing path: $path" >&2; exit 1; }
done
if brew list --formula | grep -qx ianchor; then echo 'iAnchor already installed' >&2; exit 1; fi
work=$(mktemp -d "$RUNNER_TEMP/ianchor-acceptance.XXXXXX")
server=''
cleanup() { [[ -z "$server" ]] || kill "$server" 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT
mkdir -p "$work/www" "$work/tap/Formula" "$work/old"
cp "$ASSET_DIR"/*.tar.gz "$work/www/"
# The legacy source is pinned by commit; the formula is rendered by that exact commit.
curl --fail --location --retry 3 -o "$work/www/old.tar.gz" "https://github.com/puppetdevz/ianchor/archive/$old.tar.gz"
for file in Formula/ianchor.rb scripts/release/render.rb scripts/release/metadata.rb; do
  git show "$old:$file" > "$work/old/$(basename "$file")"
done
python3 -u - "$work/www" "$work/port" <<'PY' &
import http.server, os, socketserver, sys
os.chdir(sys.argv[1])
class Server(socketserver.TCPServer):
    allow_reuse_address = True
with Server(('127.0.0.1', 0), http.server.SimpleHTTPRequestHandler) as server:
    with open(sys.argv[2], 'w') as f:
        f.write(str(server.server_address[1]))
    server.serve_forever()
PY
server=$!
for _ in {1..100}; do [[ -s "$work/port" ]] && break; kill -0 "$server"; sleep 0.1; done
[[ -s "$work/port" ]]
base="http://127.0.0.1:$(< "$work/port")"
oldsha=$(shasum -a 256 "$work/www/old.tar.gz" | awk '{print $1}')
ruby "$work/old/render.rb" "$work/old/ianchor.rb" "$work/tap/Formula/ianchor.rb" 2.0.2 "$base/old.tar.gz" "$oldsha" "$work/old/metadata.rb"
ruby -c "$work/tap/Formula/ianchor.rb"
git -C "$work/tap" init -q
git -C "$work/tap" -c user.name=Acceptance -c user.email=acceptance@example.invalid add Formula/ianchor.rb
git -C "$work/tap" -c user.name=Acceptance -c user.email=acceptance@example.invalid commit -qm old
brew tap puppetdevz/tap "file://$work/tap"
armsha=$(shasum -a 256 "$work/www/ianchor_${version}_darwin_arm64.tar.gz" | awk '{print $1}')
amdsha=$(shasum -a 256 "$work/www/ianchor_${version}_darwin_amd64.tar.gz" | awk '{print $1}')
ruby scripts/release/render.rb Formula/ianchor.rb "$work/new.rb" "$version" "$armsha" "$amdsha" scripts/release/metadata.rb
# Change only the two exact production download URLs, never Formula behavior.
ruby - "$work/new.rb" "$version" "$base" <<'RUBY'
path, version, base = ARGV
s = File.binread(path)
%w[arm64 amd64].each do |arch|
  name = "ianchor_#{version}_darwin_#{arch}.tar.gz"
  production = "https://github.com/puppetdevz/ianchor/releases/download/v#{version}/#{name}"
  abort "Expected exactly one production URL: #{production}" unless s.scan(production).length == 1
  s = s.sub(production, "#{base}/#{name}")
end
File.binwrite(path, s)
RUBY
ruby -c "$work/new.rb"
check_install() {
  local expected=$1
  [[ "$("$prefix/bin/ianchor" --version)" = "$expected" ]]
  file -L "$prefix/bin/ianchor" | grep -q "$EXPECTED_ARCH"
  brew test puppetdevz/tap/ianchor
  "$prefix/bin/ianchor" completion bash | grep -q complete
  "$prefix/bin/ianchor" completion zsh | grep -q '#compdef ianchor'
  test -s "$prefix/etc/bash_completion.d/ianchor"
  test -s "$prefix/share/zsh/site-functions/_ianchor"
}
# Fresh install first, then remove it before exercising an actual same-tap upgrade.
cp "$work/new.rb" "$tap/Formula/ianchor.rb"
brew install puppetdevz/tap/ianchor
check_install "$version"
brew uninstall puppetdevz/tap/ianchor
ruby "$work/old/render.rb" "$work/old/ianchor.rb" "$tap/Formula/ianchor.rb" 2.0.2 "$base/old.tar.gz" "$oldsha" "$work/old/metadata.rb"
brew install --build-from-source puppetdevz/tap/ianchor
check_install 2.0.2
cp "$work/new.rb" "$tap/Formula/ianchor.rb"
brew upgrade puppetdevz/tap/ianchor
check_install "$version"
echo "Verified $TAG ($goarch), fresh install and upgrade from 2.0.2"
