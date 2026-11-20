#!/usr/bin/env bash
# Build or verify the exact pair of release artifacts. Never writes into the checkout.
set -euo pipefail
command=${1:?build or verify}; dir=${2:?asset directory}; tag=${3:?tag}
[[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo 'Invalid stable tag' >&2; exit 1; }
version=${tag#v}
[[ "$(tr -d '[:space:]' < VERSION)" = "$version" ]] || { echo 'Tag/version mismatch' >&2; exit 1; }
ruby -e 'a=ARGV[0].split(".").map(&:to_i); abort "Version must exceed 2.0.2" unless (a <=> [2,0,2]) == 1' "$version"
if [[ "$command" = build ]]; then
  mkdir -p "$dir"
  dir=$(cd "$dir" && pwd)
  work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
  for arch in arm64 amd64; do
    GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -buildvcs=false -o "$work/ianchor" ./cmd/ianchor
    # Normalize tar and gzip metadata so a rerun can reproduce published bytes.
    python3 - "$work/ianchor" "$dir/ianchor_${version}_darwin_${arch}.tar.gz" <<'PY'
import gzip, os, sys, tarfile
source, output = sys.argv[1:]
info = tarfile.TarInfo('ianchor')
info.size = os.path.getsize(source)
info.mode = 0o755
info.mtime = 0
with open(output, 'wb') as dest, gzip.GzipFile(filename='', mode='wb', fileobj=dest, mtime=0) as gz:
    with tarfile.open(fileobj=gz, mode='w', format=tarfile.USTAR_FORMAT) as archive, open(source, 'rb') as binary:
        archive.addfile(info, binary)
PY
  done
  (cd "$dir" && shasum -a 256 "ianchor_${version}_darwin_arm64.tar.gz" "ianchor_${version}_darwin_amd64.tar.gz" > SHA256SUMS)
elif [[ "$command" != verify ]]; then
  echo 'Expected build or verify' >&2; exit 1
fi
# Reject extra entries, duplicate names and path traversal before shasum reads paths.
ruby -e 'v=ARGV[1]; l=File.readlines(ARGV[0], chomp: true); names=%W[ianchor_#{v}_darwin_arm64.tar.gz ianchor_#{v}_darwin_amd64.tar.gz]; abort "Invalid manifest" unless l.length == 2 && l.map { |x| m=/\A[0-9a-f]{64}  ([^\/]+)\z/.match(x); m && m[1] }.sort == names.sort' "$dir/SHA256SUMS" "$version"
(cd "$dir" && shasum -a 256 -c SHA256SUMS)
