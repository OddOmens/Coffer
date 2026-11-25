# Coffer Desktop

A desktop app for Coffer on Linux and macOS. It's a native window around your own Coffer server, built with [Tauri v2](https://v2.tauri.app).

- On first launch it asks for your server's address, for example `http://192.168.1.20:8080` or `https://files.example.com`. It checks the address by fetching `/api/config`, remembers it, and opens the web app.
- After that it goes straight to the saved server. If the server can't be reached, you get an error with **Try again** and **Change server**.
- Native menus: **Coffer → About, Change Server…, Reload (⌘R / Ctrl+R), Quit**, a standard **Edit** menu (copy and paste work on macOS), and **View → Zoom In / Zoom Out / Actual Size**.
- Downloads go to your Downloads folder. If a file with that name already exists, the new one is saved as `name (1).ext`, and you get a notification when it finishes.
- It remembers the window size and position. Launching it a second time just focuses the window that's already open.

## Security model

- The webview can only navigate inside the configured server's origin (scheme, host and port) and the bundled connect page. Any other `http(s)` or `mailto:` link opens in your default browser or mail app. A `target="_blank"` link to the server opens in a second Coffer window that shares your session.
- Pages loaded from the server can't use Tauri. The only capability, `src-tauri/capabilities/connect-page.json`, applies only to the local connect page and has no `remote` URLs. Both commands (`startup`, `connect`) also check that they were called from the bundled page.
- Downloads are only accepted from the server's origin, or from `blob:` and `data:` URLs the page creates.
- Devtools are only available in debug builds.

## Project layout

```
desktop/
  index.html, src/        connect page (plain TypeScript + CSS, built by Vite)
  src-tauri/              Rust app (window, menu, navigation policy, downloads)
  app-icon.svg            source for every icon in src-tauri/icons (npm run icons)
  scripts/build-linux.sh  Docker build for Linux
```

## Building

### Linux (.deb + AppImage) with Docker

You only need Docker. The script builds a toolchain image (Rust, the WebKitGTK dev packages and Node 22) from `scripts/Dockerfile.linux`, then builds inside it as your user:

```sh
cd desktop
./scripts/build-linux.sh          # add --test to run the Rust unit tests first
```

The builds land in `desktop/artifacts/`, for example `Coffer_0.1.0_amd64.deb` and `Coffer_0.1.0_amd64.AppImage`. Caches are kept in `~/.cache/coffer-desktop-{cargo,npm,home,target}`, so later builds are much faster. Delete those folders to start from scratch.

Install the `.deb` with `sudo apt install ./Coffer_0.1.0_amd64.deb`. To use the AppImage, run `chmod +x Coffer_*.AppImage` and then run the file.

### macOS (locally)

You need the Xcode command line tools (`xcode-select --install`), Rust (via [rustup](https://rustup.rs)) and Node 22 or later.

```sh
cd desktop
npm ci
npm run tauri build                      # builds for this Mac's architecture
# or a universal build (Apple silicon + Intel):
rustup target add aarch64-apple-darwin x86_64-apple-darwin
npm run tauri build -- --target universal-apple-darwin
```

The `.app` and `.dmg` end up under `src-tauri/target/…/release/bundle/`.

For development, `npm run tauri dev` runs a debug build with devtools enabled.

### CI (GitHub Actions)

`.github/workflows/desktop.yml` builds on Linux (ubuntu-22.04: `.deb` and AppImage) and macOS (universal `.dmg`). Every Coffer release runs it and attaches the apps to that release, so the desktop app shares the server's version number (`scripts/release-version.sh` sets it; see [RELEASING.md](../RELEASING.md)).

You can also start it by hand from the Actions tab (**Run workflow**): leave the tag empty to just build (the apps are kept as workflow artifacts for 14 days), or enter an existing tag such as `v0.3.0` to rebuild and attach them to that release.

## Opening an unsigned macOS build

Unless signing secrets are configured, the macOS build is only ad-hoc signed and isn't notarized, so Gatekeeper blocks the first launch. To allow it:

- **macOS 14 and earlier:** in Finder, right-click (or Control-click) **Coffer.app**, choose **Open**, then **Open** again.
- **macOS 15 and later:** try to open the app once, then go to **System Settings → Privacy & Security** and click **Open Anyway** next to the message about Coffer.
- **Or, in Terminal**, after you copy the app to Applications:

  ```sh
  xattr -dr com.apple.quarantine /Applications/Coffer.app
  ```

You only need to do this once per downloaded version.

## Optional: code signing and notarization

The workflow signs and notarizes the macOS build when these repository secrets exist. If they're missing, it falls back to the ad-hoc signed build described above. Variables that are unset are never passed to the build.

| Secret | What it is |
| --- | --- |
| `APPLE_CERTIFICATE` | Base64 of your "Developer ID Application" certificate exported as `.p12` (`base64 -i cert.p12`) |
| `APPLE_CERTIFICATE_PASSWORD` | Password for that `.p12` |
| `APPLE_SIGNING_IDENTITY` | e.g. `Developer ID Application: Odd Omens LLC (TEAMID)` |
| `APPLE_ID`, `APPLE_PASSWORD`, `APPLE_TEAM_ID` | Notarization with an Apple ID and an app-specific password |
| `APPLE_API_ISSUER`, `APPLE_API_KEY`, `APPLE_API_KEY_P8` | Or notarization with an App Store Connect API key (`APPLE_API_KEY_P8` holds the contents of the `.p8` file) |

These are the same environment variables the Tauri bundler reads, so you can also export them for a signed local build. Locally, set `APPLE_API_KEY_PATH` to the `.p8` file instead of using `APPLE_API_KEY_P8`.

## Where settings live

The saved server is kept in `settings.json` in the app data folder:

- Linux: `~/.local/share/com.oddomens.coffer/`
- macOS: `~/Library/Application Support/com.oddomens.coffer/`

To switch servers, use **Coffer → Change Server…**. Deleting that file also resets the app.

## License

AGPL-3.0-only. Copyright © 2026 Odd Omens LLC.
