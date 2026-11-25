# Contributing

Suggestions and fixes are welcome.

1. **Fork** the repository and make your change on a branch in your fork.
2. Run the checks locally:
   - `go test ./...` in `backend/` (Go 1.27). Set `COFFER_TEST_S3_ENDPOINT`, `_BUCKET`, `_KEY` and `_SECRET` to also test against an S3 server.
   - `npm run lint && npm run build` in `frontend/`.
   - For the desktop app, `npm run build:web && cargo test --manifest-path src-tauri/Cargo.toml` in `desktop/`.
3. Open a **pull request** against `main`, saying what it changes and why. Add a line to `CHANGELOG.md` under **Unreleased** for anything a user would notice.

Coffer's security model is short and strict, so please keep to it:

- Every file operation goes through `files.FS` (`os.Root` for local drives). No direct `os.Open` on user paths.
- The share listener (port 8081) only serves share routes. Anything new there must need a share token.
- File types come from extensions, never content sniffing, and file responses stay sandboxed.

How it's reviewed:

- Only the maintainer can merge. Every pull request needs their approval, and the CI checks (backend tests, frontend build, desktop tests, Docker image builds) must pass.
- CI doesn't run on a contributor's pull request until the maintainer approves the run.
- Pushing new commits after an approval needs a fresh approval.

Coffer is licensed under the [GNU AGPL v3.0](LICENSE). By opening a pull request you agree that your contribution is licensed under it too.

For a bug or an idea you'd rather not code yourself, open an issue. For a **security problem**, don't open an issue or pull request; see [SECURITY.md](SECURITY.md).
