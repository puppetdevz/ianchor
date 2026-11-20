# Release asset CLI contract

`publish.sh` requires `TAG`, `EXPECTED_COMMIT`, `SOURCE_TOKEN`, `TAP_TOKEN`, and
`ASSET_DIR`. `TAG` must be the stable tag matching the checked-out `VERSION`.
`ASSET_DIR` must contain `ianchor_${VERSION}_darwin_arm64.tar.gz` and
`ianchor_${VERSION}_darwin_amd64.tar.gz`. These are prebuilt inputs: the script
never builds or rewrites them. Each must be a regular, non-symlink gzip tar
archive, with a root-level executable `ianchor`: a 64-bit Mach-O executable
for the named architecture. The validator verifies tar checksums, complete gzip
and tar streams, unique safe root-level paths, and regular files/directories
only; links, extended tar headers and nested paths are not accepted. Additional
root-level regular files or empty directories are allowed. Individual entries
are limited to 1 GiB. Create archives without platform-specific metadata or
extended headers (for example, `COPYFILE_DISABLE=1 tar --format ustar -czf
ARCHIVE -C DIRECTORY ianchor` on macOS).

After checking the tap version, the script snapshots and validates both inputs,
checks existing Formula identity, and then creates or finds the release. It
lists all asset pages and never uses `--clobber`. Existing names must be fully
uploaded and match the local SHA256 when fetched without credentials from
`https://github.com/puppetdevz/ianchor/releases/download/${TAG}/${name}`. For
missing names it attempts `gh release upload`; even after a failed upload it
re-queries and accepts only a present asset with matching public bytes. Both
public downloads must verify before a tap update. Tag checks, the downgrade
check and the tap compare-and-swap remain in force. Historical releases may
receive assets but cannot downgrade the tap.

The companion interfaces are `ruby render.rb TEMPLATE OUT VERSION ARM_SHA
AMD_SHA PARSER` and `ruby check-existing.rb FORMULA TARGET PARSER [ARM_SHA
AMD_SHA]`. The latter is called once without and once with hashes; status 3
means the tap is newer. These interfaces and the Formula template must be
updated together before this asset-enabled publisher is used.
