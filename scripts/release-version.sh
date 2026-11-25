#!/bin/sh
# Sets the version everywhere it is written down, ready for a release:
#   scripts/release-version.sh 0.4.0
# The server binary gets its version from the tag at build time, so only the
# web, desktop and changelog files need it. See RELEASING.md.
set -eu
v="${1:-}"
case "$v" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "usage: $0 X.Y.Z" >&2; exit 1 ;;
esac
cd "$(dirname "$0")/.."

# perl rather than sed -i, which differs between Linux and macOS.
export V="$v"
# The first "version" in each package/config file is the app's own.
for f in frontend/package.json desktop/package.json desktop/src-tauri/tauri.conf.json; do
  perl -0pi -e 's/"version": "[^"]+"/"version": "$ENV{V}"/' "$f"
done
# package-lock.json has the root package's version twice at the top.
for f in frontend/package-lock.json desktop/package-lock.json; do
  perl -0pi -e '$n = 0; s/"version": "[^"]+"/$n++ < 2 ? "\"version\": \"$ENV{V}\"" : $&/ge' "$f"
done
perl -0pi -e 's/^version = "[^"]+"/version = "$ENV{V}"/m' desktop/src-tauri/Cargo.toml
perl -0pi -e 's/(name = "coffer-desktop"\nversion = ")[^"]+"/$1$ENV{V}"/' desktop/src-tauri/Cargo.lock

if ! grep -qs "^## \[$v\]" CHANGELOG.md; then
  echo "Now move the notes under '## [Unreleased]' in CHANGELOG.md to '## [$v] - $(date +%Y-%m-%d)'."
fi
echo "Version set to $v. Commit, then: git tag v$v && git push origin v$v"
