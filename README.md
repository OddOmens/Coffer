# Coffer

A fast, locked-down, self-hosted file browser for creative teams. Browse, preview, edit and share files from any browser, install it as an app (PWA, macOS, Linux), mount it in Finder, Explorer or Nautilus, and work on the same files from After Effects over SMB.

Coffer is one of three self-hosted apps from Odd Omens that are made to work together: [Quire](https://github.com/OddOmens/Quire) for notes and documents, **Coffer** for files, and [Docket](https://github.com/OddOmens/Docket) for tracking work. Quire backs itself up into Coffer.

- **Multiple drives.** Local folders and S3-compatible buckets (Cloudflare R2, AWS S3, Backblaze B2, MinIO) side by side, with the same browsing, previews and sharing. R2 drives can serve downloads straight from the bucket edge.
- **Instant search.** Every drive is indexed (SQLite FTS5 trigram), so substring search returns results as you type, across all drives, filtered by type. Press ⌘K / Ctrl K anywhere.
- **Folder sizes.** Recursive sizes and file counts for every folder, plus a storage breakdown by type.
- **Sharing that fits client work.**
  - **Share by email, no accounts.** Send a link to people's inboxes. Choose who can open it: anyone with the link, anyone who confirms their email with a one-time code, or only the people you invited. You see who opened and downloaded what, and can remove one person without breaking the link for everyone else.
  - **Send files** in one step, like WeTransfer: drop files, add emails, done. They land in *Sent* in your files.
  - **Request files** from anyone by email with a drop box they upload into.
  - **Review and approval.** Recipients comment at a video timecode or on a spot in an image, and approve or ask for changes. Your team replies in the app, with internal notes the client never sees.
  - **Versions.** `spot_v11.mov`, `spot_v12.mov`… stack into one row, and a link can always show the newest version.
  - **Collections** gather files from anywhere into one link, without moving them.
  - **Gallery view** shows a folder or collection link as a grid of photos with a full-screen viewer.
  - **Watermarks** burn the viewer's email into image previews and show it over videos and documents.
  - **QR codes** for any link.
  - **Links** with presets (Review, Deliver, View only, Request files, Collaborate) and per-link options:
    - view, download and upload permissions, set independently
    - password, expiry date and download limit
    - network allowlist and a pause switch
    - recipient title and message
    - upload rules: file types, size limit, "ask for the uploader's name"
    - owner notifications when the link is opened, downloaded from or uploaded to
  - **People:** share folders with other users; they appear under *Shared with me*. Access ends automatically when the sharer loses theirs.
- **Video and photo previews.** A media worker makes browser-playable H.264 copies of ProRes, HEVC, MXF and other formats, video thumbnails, and previews of HEIC and camera RAW photos.
- **Team tools.** Lock a file while you work on it (and see files open over SMB in After Effects), favourites in the sidebar, shared tags, and an activity feed for every folder.
- **Storage limits** per person.
- **Backups.** Nightly, deduplicated, versioned backups of a folder to another drive (an R2 bucket is ideal), with point-in-time restore from the admin panel.
- **Encrypted buckets.** Turn on *Encrypt files* for an S3, R2 or B2 drive and Coffer encrypts everything before it leaves the server. You still browse, preview and share those files on every device as usual; the provider only stores unreadable data.
- **Office and Google files.**
  - **In-browser previews:** Word (.docx), Excel, CSV and ODS sheets, PowerPoint slide text and images, PDF, images, video and audio.
  - **Google shortcuts:** .gdoc, .gsheet and .gslides files open in Google.
  - **Editor:** a built-in text, code, Markdown and CSV editor with live preview and conflict detection.
- **Connect from desktop apps.**
  - **WebDAV** at `/dav/` for Finder, Explorer, Nautilus and rclone, using per-device app passwords.
  - **SMB** (Samba sidecar) for After Effects speed on the LAN.
  - **Desktop apps** for macOS and Linux (see `desktop/`).
- **Security first.**
  - Every file operation goes through `os.Root`, and share links run on their own listener.
  - Argon2id passwords, TOTP two-factor with recovery codes, and an optional *require 2FA* policy.
  - Strict CSP, sandboxed file responses and a full audit log.
  - Distroless, read-only and non-root container. See [Security](#security).
- **Monitoring and updates.** A live dashboard (requests, bandwidth, CPU, memory, drives, sessions, link activity), Prometheus `/metrics`, and an in-app notice when a new release is out.
- **Customisable.** Name, logo, accent colour and sign-in message. Light and dark themes, compact or comfortable density, list or grid, per-user accent and page size.

Built with Go 1.27 and React + [shadcn/ui](https://ui.shadcn.com). The image is about 33 MB.

**You need** Docker with Compose on a Linux machine (x86-64 or ARM64). Coffer is built for small teams on their own server: one admin, many users, and share links for clients.

---

## Quick start

```sh
git clone https://github.com/OddOmens/Coffer.git coffer && cd coffer
cp .env.example .env                          # set COFFER_FILES_DIR, COFFER_DATA_DIR, PUID/PGID
cp samba/users.conf.example samba/users.conf  # SMB logins: one "user:password" per line
docker compose up -d
```

This pulls the released images (`ghcr.io/oddomens/coffer`, `coffer-media` and `coffer-samba`, for x86-64 and ARM64). To build from source instead, run `docker compose up -d --build`.

Open `http://<server>:8080` and sign in as `admin`. If you didn't set `COFFER_ADMIN_PASSWORD`, the password is in `docker compose logs coffer`. Then turn on two-factor under **Settings → Security**.

| Port | Serves | Expose to |
| --- | --- | --- |
| 8080 | Full app, API and WebDAV (`/dav/`) | LAN or VPN; HTTPS via your proxy if remote |
| 8081 | Share links only (no sign-in, no admin, no WebDAV) | The internet, via Cloudflare Tunnel or a reverse proxy |
| 445 | SMB for desktop apps | LAN only. **Never** the internet |

## Drives

**Admin settings → Drives → Add drive.**

- **Local folder:** mount it into the container under `/srv` (see the commented example in `docker-compose.yml`), then add `/srv/<name>`. To reach it over SMB as well, mount it into the `samba` service under `/shares/<Name>`.
- **Cloudflare R2:**
  1. In the Cloudflare dashboard go to **R2 → Manage API tokens** and create a token with *Object Read & Write* on the bucket.
  2. In Coffer, pick the **Cloudflare R2** preset and enter `<ACCOUNT_ID>.r2.cloudflarestorage.com`, the bucket, the access key ID and the secret. Region is `auto`.
  3. Turn on **Direct downloads** so files come straight from R2's edge (R2 has no egress fees).

  Secrets are stored encrypted with the instance key. R2 drives work in the web app, share links and WebDAV, but not over SMB.
- **Backblaze B2:**
  1. Paste the **Endpoint** shown on your bucket in Backblaze, such as `s3.us-east-005.backblazeb2.com`. Coffer reads the region from it.
  2. Under **Application Keys**, make a key for that bucket only, with Read and Write. Its keyID is the access key and its applicationKey the secret.
  3. In the bucket's **Lifecycle Settings**, choose **Keep only the last version of the file**. B2 keeps deleted files as hidden versions, so without this, emptied trash and pruned backups are still stored and billed.

### Encrypted buckets

Turn on **Encrypt files** when you add an S3, R2 or Backblaze B2 drive, and Coffer encrypts each file before it leaves the server. Every signed-in device (browser, iPhone, the desktop apps, WebDAV) still gets ordinary files, because Coffer decrypts them on the way out. Previews, search, share links and video scrubbing all work as before. Only the bucket provider, or anyone else who gets into the bucket, sees nothing readable.

- **Save the recovery key.** Coffer shows it once when you add the drive, and again under the drive's **⋯ → Show recovery key** (needs your password). Coffer keeps its own copy, sealed with `secret.key`, but if the server and its data folder are lost, the recovery key is the only way to read the bucket. That includes backups stored in it. Keep it in a password manager, not on the server.
- **Moving to a new server, or recovering from a lost one:** add the drive again with the same bucket and folder, turn on **Encrypt files**, and paste the recovery key. To get backups back, create a backup under **Admin → Backups** with that drive and the same target folder. Its snapshots appear straight away, ready to restore, and later runs carry on from them.
- Encrypted drives show a lock in the sidebar and on **Admin → Drives**. A file that fails its check, because it was changed in the bucket or put there some other way, is refused with "this file is damaged" and logged.
- **Encryption is chosen when the drive is added.** It needs an empty bucket or folder (prefix), and Coffer won't mix encrypted and plain files. To encrypt files you already have in a bucket, add a new encrypted drive on a new folder and move them across.
- **Direct downloads** are off for encrypted drives, so downloads go through Coffer. Video scrubbing and resumed downloads only fetch the parts they need.
- **What stays visible** in the bucket: file and folder names, the folder layout, exact file sizes and upload times. Backups into an encrypted drive hide names too.
- **How it works:** each file is split into 64 KiB segments, each sealed with AES-256-GCM under its own random nonce and a per-file key derived (HKDF-SHA256) from a random 256-bit drive key. Segments can't be changed, reordered, dropped or moved between files without Coffer noticing; a file that fails the check is refused, never served. Whole files could still be swapped for each other, or for an older version of themselves, by someone with write access to the bucket.

Every drive is indexed in the background (local drives every 15 minutes, buckets every 6 hours by default). Changes made through Coffer show up immediately; **Rescan** catches changes made elsewhere.

## Email

Invitations, sign-in codes for links and email notifications need an SMTP server. Set it in **App settings → Email** and press **Send test email**.

- **Proton:** some paid plans with a custom domain can create SMTP tokens (Proton Mail → Settings → IMAP/SMTP). If yours can, use `smtp.protonmail.ch`, port 587, STARTTLS, your address as the username and the token as the password.
- Any transactional provider works too (Postmark, Resend, Amazon SES, Mailgun…).

People set their own address under **Settings → General** to get notifications by email.

## Previews (media worker)

The `media` service in `docker-compose.yml` runs the same Coffer binary with ffmpeg and ImageMagick. It reads your files read-only and writes previews to `DATA/media`. Previews are made when someone opens a file, and for new uploads straight away (turn that off in **App settings → Previews**). Limit its CPU with `COFFER_MEDIA_CPUS`. Without it, everything else works; previews just say they're unavailable.

Mount extra local drives into it at the same `/srv/...` paths as in the `coffer` service.

## Desktop access

| | How |
| --- | --- |
| **After Effects, LAN** | Finder ⌘K → `smb://<server>/Files` (one share per drive) |
| **Finder, anywhere** | ⌘K → `https://<host>/dav/`, using your username and an app password (**Settings → Connect**) |
| **Windows** | Map network drive → `https://<host>/dav/` |
| **Linux** | Nautilus or Dolphin → `davs://<host>/dav/` |
| **rclone** | `webdav` remote, vendor `other`, URL `https://<host>/dav/` |
| **App** | Install the PWA from the browser (address bar → Install), or the macOS / Linux desktop app from the [releases page](https://github.com/OddOmens/Coffer/releases) |

WebDAV deletes go to Coffer's trash, and so does the old version of a file saved over (which, as in the web app, needs permission to delete there). App passwords can be revoked one at a time, are all revoked when you change your password, and never unlock the web app.

## Sharing publicly

Share links work from anywhere once port **8081** is reachable over HTTPS. The easiest way is a Cloudflare Tunnel: no open ports on your router.

1. In the Cloudflare dashboard go to **Zero Trust → Networks → Tunnels → Create a tunnel** (type *Cloudflared*). Copy the token.
2. Add a **public hostname**, e.g. `files.example.com`, with service `http://coffer:8081`.
3. In `.env` set:
   ```sh
   CLOUDFLARE_TUNNEL_TOKEN=<token>
   COFFER_PUBLIC_URL=https://files.example.com
   ```
4. `docker compose --profile tunnel up -d`
5. Right-click a file or folder → **Share…**

Only share routes are served on 8081, so the tunnel never exposes sign-in, admin or WebDAV. For the full app away from the office (and HTTPS for WebDAV in Finder and PWA install), use a VPN such as Tailscale and set `COFFER_APP_NETWORKS` to your LAN and VPN ranges.

**Visitor addresses.** Coffer needs each visitor's real address for link network allowlists, rate limits and the audit log, and only takes it from a proxy it trusts. The tunnel runs on its own small Docker network at the fixed address `172.30.80.2`, which `docker-compose.yml` already trusts on port 8081, so there's nothing to set.

- **Upgrading from an older setup?** If `.env` has `COFFER_TRUSTED_PROXIES` set to Coffer's whole Docker subnet (such as `172.18.0.0/16`), remove it. A subnet also holds the network's gateway, which connections from the host or through a published port come from, so anything that could reach Coffer that way could claim any address. It keeps working until you do, but only on port 8081.
- **Your own reverse proxy in front of 8081:** set `COFFER_TRUSTED_PROXIES` to its exact address, not a range. For a proxy on the host, that's the gateway of Coffer's network (`docker network inspect coffer_default`); keep `COFFER_PUBLIC_BIND=127.0.0.1` so only the host can reach 8081.
- **A reverse proxy in front of 8080** (the full app) is trusted separately, with `COFFER_APP_TRUSTED_PROXIES`. Port 8080 ignores forwarding headers otherwise.

Recipients only ever see the shared item. Paused, expired, revoked or orphaned links all stop working immediately.

## Users and access

Each user gets one or more **home folders** on any drive, each with its own permissions (upload/edit, delete, share) and an optional storage limit. Users can also share folders with each other. Admins see every drive.

## Backups

**Admin settings → Backups → New backup.** Pick a source folder and a target drive (an R2 bucket is ideal: no egress fees; make it an [encrypted bucket](#encrypted-buckets) to keep the provider out of your files, names included). Each night, files that changed since the last run are uploaded; unchanged ones are kept by reference, so every snapshot is complete but only changes cost space. Coffer keeps the number of snapshots you choose, and **Restore** puts any file or folder from any snapshot into a new "Restored …" folder next to the original.

**To look inside a backup**, choose **Browse snapshots** from its ⋯ menu. Pick a snapshot by date and click through its folders as they were then. Files preview and play straight from the backup drive, and you can download one file, a selection or a whole folder (as a zip) without restoring anything first.

**To back up only some files and folders**, select them in **Files** and click **Back up** (also in the right-click menu). Add them to an existing backup or start a new one. They're kept by their full path, so `03. Studio/001 - Documents` is restored as `Restored …/03. Studio/001 - Documents`. Backups follow folders renamed or moved in Coffer. If a picked folder disappears another way, the next run says what it couldn't find. To take something out again, select it and choose **Stop backing up**, or click the × beside it under **Admin → Backups**.

**Coffer's own database and key** go into each backup too, under `_coffer/<snapshot date>/` in the backup's folder on the target drive (the newest 7 are kept), so Coffer itself can be restored, with its users, links, drives and settings. `secret.key` unlocks the stored passwords and drive keys, so by default the copy is only made when the target is a local drive or an [encrypted bucket](#encrypted-buckets); anyone who can read that folder on the disk or in the bucket can read the copy. Inside Coffer the `_coffer` folder is hidden: only admins browsing the drive with **Show hidden files** on can open it, share links never serve it, and a backup whose folder another user can open doesn't get the copy at all (admins are notified). Change it under **App settings → Health and backups** (every backup, or never). If you set `COFFER_SECRET_KEY`, the key isn't copied: keep it yourself.

To restore Coffer itself:

1. `docker compose down`
2. Copy `coffer.db` and `secret.key` from the newest `_coffer/<date>/` folder into the data folder (`COFFER_DATA_DIR`), replacing what's there, and delete `coffer.db-wal` and `coffer.db-shm` if present. From an encrypted bucket, add the bucket as a drive on a fresh Coffer with its recovery key, turn on **Show hidden files** and download the two files from `.backups/<name>/_coffer/` first.
3. `docker compose up -d`. The search index rebuilds itself.

Security options under **App settings**:

- require 2FA for admins or for everyone
- require a password on every link
- maximum link expiry
- per-file upload limits for links

Locked out? Run `docker compose exec coffer /coffer reset-password admin`. If no admin is left, `reset-password --admin <user>` also makes that user an admin again.

## Health alerts

Every 5 minutes Coffer checks for a drive that went offline, less than 10% free space on a local drive or the data folder (critical below 3%), a backup without a good run for 48 hours, a media worker that stopped while previews wait, search scans failing 3 times in a row, and email that can't be sent. Admins get a notification when a problem starts and when it's fixed, and an email too if they've added an address (turn emails off under **App settings → Health and backups**). A problem that lasts is repeated at most once a day. Current problems are listed on Home and under **Admin settings → Monitoring**.

Coffer also tidies up once a day: previews nobody opened for 60 days, and the least recently opened beyond the limit in **App settings → Previews** (20 GB by default); preview jobs finished more than 30 days ago; backup run history older than a year (keeping the latest 50 per backup); links 90 days after they expired or were turned off; and folders made by **Send files**, which go to the trash 30 days after their link expired, unless something in them changed after they were sent.

## Configuration

See `.env.example`. Key variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `COFFER_VERSION` | `latest` | Image tag to run (`latest`, `0.3.0`, `0.3`) |
| `COFFER_FILES_DIR` / `COFFER_DATA_DIR` | — | Main drive and app data (database, index, key). **Back up the data folder.** |
| `COFFER_PUBLIC_URL` | — | Base URL for share links |
| `COFFER_TRUSTED_PROXIES` | — | Proxies allowed to report the visitor's address on 8081, as exact addresses (the bundled tunnel is already trusted) |
| `COFFER_APP_TRUSTED_PROXIES` | — | The same for a reverse proxy in front of 8080 |
| `COFFER_APP_NETWORKS` | — | Restrict the full app on 8080 to these CIDRs |
| `COFFER_DRIVE_ROOTS` | `/srv` | Where admins may add local drives from |
| `COFFER_METRICS_TOKEN` | — | Enables `/metrics` (bearer token) |
| `COFFER_UPDATE_CHECK` | `true` | Check GitHub releases (sends only the version as User-Agent) |
| `COFFER_SESSION_IDLE` / `COFFER_SESSION_MAX` | `168h` / `720h` | Session lifetimes |

## Updating

Run `docker compose pull && docker compose up -d` (or `git pull && docker compose up -d --build` from source). Admins are notified in the app when a new version is out, and database migrations run automatically on start. Back up the data folder before a big jump.

`COFFER_VERSION` in `.env` picks the image tag: `latest`, an exact version such as `0.3.0`, or `0.3` to only take 0.3.x fixes. Release notes are in [CHANGELOG.md](CHANGELOG.md) and on the [releases page](https://github.com/OddOmens/Coffer/releases), which also has the desktop apps.

## Security

- **Filesystem confinement:** every local operation goes through Go's [`os.Root`](https://pkg.go.dev/os#Root); `../`, absolute paths and escaping symlinks can't leave a drive. User places, grants and share roots are enforced on top, and grants are re-validated against the granter on every request.
- **Separate public surface:** port 8081 only has share routes.
- **Sessions:** 256-bit tokens stored as hashes; `HttpOnly`, `SameSite=Strict`, `__Host-` cookies over HTTPS. Revoked on password, role or folder changes.
- **Passwords and 2FA:** Argon2id with bounded concurrency, constant-time checks, per-IP rate limits and per-account lockout. TOTP with replay protection and single-use recovery codes. Optional enforced enrolment.
- **WebDAV:** app passwords only (random 144-bit, stored hashed), with failed sign-ins backed off per IP. Responses carry a sandbox CSP.
- **CSRF:** Fetch Metadata and Origin checks on every state-changing request.
- **Encrypted buckets:** AES-256-GCM in authenticated 64 KiB segments, with per-file keys and a 256-bit drive key that is sealed with the instance key and backed by a recovery key. See [Encrypted buckets](#encrypted-buckets).
- **Share links:** 256-bit tokens, hashed for lookup and AES-GCM-sealed for redisplay. Rate-limited probes, Argon2id link passwords of at least 10 characters with a per-link guess limit, network allowlists. Hidden files inside a shared folder can't be opened, zipped or uploaded through the link. Each link's visitors only see their own link's review threads (and the team's), never another client's. Visitors can't flood the owner: download and upload notifications are grouped (one per link every 10 minutes, with a count), a link's emails are capped per hour, and each link and visitor can only have 10 uploads open at once.
- **Forwarded addresses** are only taken from the proxies trusted on that port: the tunnel's fixed address on 8081, and on 8080 only what `COFFER_APP_TRUSTED_PROXIES` names.
- **Email access:** one-time codes are six digits, valid 15 minutes, limited to five tries and rate-limited per link and address (three codes per address an hour); asking for a code never reveals who was invited. Invitation links carry a signed, expiring token. Removing a person ends their access immediately.
- **Watermarks** are burned into image previews on the server, which are smaller than the original. Videos, PDFs, documents and text are sent as they are, with the watermark drawn over them in the browser: a deterrent, not protection. Turn off downloads too for sensitive work.
- **Media worker:** a separate read-only container. Input formats are fixed from the file extension and ffmpeg may only read local files, so a crafted file can't pull in others; ImageMagick runs with memory, disk and time limits.
- **Untrusted content:** types come from extensions, never sniffing. Only a short allow-list renders inline; everything else downloads. Every file response is sandboxed. Office and Markdown previews render client-side with links sanitised (DOMPurify), and the strict CSP blocks scripts from documents.
- **Container:** distroless, non-root, read-only, all capabilities dropped. Samba is SMB3 and NTLMv2 only, with no guests.
- **Audit log:** sign-ins, changes and every public download and upload, kept for a year.

See [SECURITY.md](SECURITY.md) for reporting issues and a deployment checklist.

## Development

```sh
cd backend && go test ./...            # add COFFER_TEST_S3_* to also test against an S3 server
cd frontend && npm install && npm run dev
cd desktop && ./scripts/build-linux.sh # desktop app, see desktop/README.md
```

Pull requests run the backend tests (with the race detector and an S3 server), the frontend build, the desktop tests and a boot test of the images. See [CONTRIBUTING.md](CONTRIBUTING.md), and [RELEASING.md](RELEASING.md) for how versions are published.

## License

[AGPL-3.0-only](LICENSE) © Odd Omens LLC
