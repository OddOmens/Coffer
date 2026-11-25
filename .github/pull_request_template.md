## What this changes

<!-- What it does and why. Link the issue it fixes, e.g. "Fixes #12". -->

## Checklist

- [ ] `go test ./...` passes in `backend/`
- [ ] `npm run build` passes in `frontend/`
- [ ] Every file operation still goes through `files.FS`, and nothing new is reachable on the share listener (8081) without a share token
- [ ] A line in `CHANGELOG.md` under **Unreleased**, if a user would notice the change
- [ ] Docs updated, if it changes how something works
