# Releasing

Coffer uses [semantic versioning](https://semver.org). The server, the images and the desktop apps share one version number, and a release is a `vX.Y.Z` tag.

1. Make sure `main` is green in CI.
2. Set the version everywhere it's written down:

   ```sh
   scripts/release-version.sh 0.4.0
   ```

3. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [0.4.0] - YYYY-MM-DD`, add a fresh empty `## [Unreleased]` above it, and update the links at the bottom.
4. Commit (`Release 0.4.0`), push, and tag:

   ```sh
   git tag v0.4.0 && git push origin main v0.4.0
   ```

Pushing the tag runs `.github/workflows/release.yml`, which:

- checks the tag matches every version file and has a CHANGELOG section,
- builds `ghcr.io/oddomens/coffer`, `coffer-media` and `coffer-samba` for x86-64 and ARM64 (the build runs the Go tests) and pushes them as `:0.4.0`, `:0.4` and `:latest`, with SBOM and provenance attestations,
- creates the GitHub release with the CHANGELOG section as its notes. Running servers check this release daily and show admins the notes,
- builds the macOS and Linux desktop apps and attaches them to the release (about 15 minutes after the rest).

A tag with a suffix (`v0.4.0-beta.1`) is published as a pre-release: only its exact image tag is pushed, and servers don't offer it as an update.

Only admins can push `v*` tags (the "Protect release tags" ruleset). If a desktop build fails, rerun it from **Actions → Desktop → Run workflow** with the tag.
