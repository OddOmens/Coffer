# Changelog

All notable changes to Coffer. Versions follow [semantic versioning](https://semver.org); each release's section here becomes its GitHub release notes, which is also what running servers show admins under "update available".

## [Unreleased]

## [0.4.0] - 2026-11-25

### Added

- **Encrypted buckets.** Turn on *Encrypt files* when adding an S3, R2 or Backblaze B2 drive, and Coffer encrypts every file before it leaves the server. You still see, preview and share the files on every device as usual. Each drive has a recovery key for moving to a new server, shown once when the drive is added and again on request with your password. Backups into an encrypted drive hide file names too. Encrypted drives show a lock in the sidebar, and the backup form warns when a bucket target isn't encrypted.
- **Backup status in the file browser.** Files and folders show whether a backup holds them as they are now: backed up, waiting for the next run (new or changed since, checked against the snapshot itself), or a folder with backed-up folders inside. Hover the icon to see which drive holds the copies and when the backup last ran. The folder header says the same for the folder you're in.
- **Browse snapshots.** Open any backup snapshot as a read-only folder view (Admin settings → Backups → ⋯ → Browse snapshots): pick a date, click through the folders as they were, preview and play files straight from the backup drive, and download a file, a selection or a whole folder as a zip, or restore them, without restoring everything first.
- **Back up from the file browser.** Select files and folders, click **Back up**, and add them to a backup or start a new one, with their full folder path kept. Backups follow renames and moves, warn when a picked folder has gone missing, and list what they hold under Admin → Backups, where items can be taken out again.
- **Backup folders open as snapshots.** Opening a backup's own folder (such as `.backups/Business` on the backup drive) takes admins straight to Browse snapshots, newest first: the same files by their real names, decrypted as you open them, instead of the coded object names stored there.
- **Show hidden files for yourself.** Settings → Show hidden files lists dot-folders such as a drive's `.backups` and system files in your own view, while the admin's "Hide hidden and system files" keeps them out of everyone else's listings and every share link.
- **Backblaze cost calculator.** Admin settings → Drives → **Backblaze costs** estimates what B2 charges to store an amount per day, month and year, and the smallest daily storage cap that holds it, with one-click amounts for each Backblaze drive. Backblaze drive cards show their monthly cost. The rate is the one Backblaze's Caps & Alerts page shows, 53 GB per $0.01 a day, and can be changed.
- **Local and Cloud sections** in the sidebar. Hover a drive's icon to see which drive it is and whether it's encrypted.
- **Health alerts.** Every 5 minutes Coffer checks for a drive that went offline, a local drive or the data folder under 10% free (critical under 3%), a backup with no good run for 48 hours, a media worker that stopped while previews wait, search scans failing 3 times in a row, and email that can't be sent. Admins are notified when a problem starts and when it's fixed, at most once a day while it lasts, and by email too when email is set up. Current problems are listed on Home for admins and on the Monitoring page, each linking to where it's fixed. New setting: **App settings → Health and backups → Email admins about problems** (on by default).
- **Backups carry Coffer itself.** Each backup run also stores a consistent copy of Coffer's database and `secret.key` under `_coffer/` in its folder (newest 7 kept), so a whole server can be restored; the README says how. New setting: **App settings → Health and backups → Copy Coffer's database and key into backups**, by default only into local drives and encrypted buckets (or every backup, or never).
- **Daily tidy-up** of what used to grow forever: previews nobody opened for 60 days and the least recently opened beyond a size limit (new setting **App settings → Previews → Keep previews up to**, 20 GB by default), preview jobs finished over 30 days ago, backup run history older than a year (the latest 50 per backup are kept), links 90 days after they expired or were turned off, and folders made by Send files, which move to the trash 30 days after their link expired unless something in them changed since.

### Changed

- **New app icon.** A white vault on solid black, in the browser tab, on the Home Screen and in the desktop apps. The mark in the sidebar and on the sign-in page matches it in both themes.
- **User settings** and, for admins, **Admin settings** are in the account menu at the foot of the sidebar. Admin settings is one page with a tab for each section (users, drives, monitoring, backups, app settings and the audit log), laid out like User settings, in place of a separate Admin section.
- **A calmer, flatter look.** Neutral grays with a blue accent in place of violet, near-black buttons, borders instead of shadows and smaller corners, in both themes. Your account, search and notifications sit together at the foot of the sidebar, with a slim bar across the top for the sidebar toggle and the sidebar flat beside the page instead of the page floating on a card. The storage card is gone from the sidebar; Home still shows how much each drive holds. Servers still on the old violet default switch to the new blue; an accent you picked yourself stays. On the dark theme, the Home page's quick actions are each lit in their own colour.
- **Backups carry on past a file they can't copy.** A file that fails (such as a Backblaze "internal incident") is tried three times, then skipped: the run finishes, the snapshot keeps the file's last backed-up version, the run and a notification list what was skipped, and the next run tries it again. A run only stops if 20 files in a row fail, when the target is likely down.
- Two backups can no longer share a target folder (each would prune the other's snapshots).
- **Backups keep older snapshots longer.** Besides the newest ones you choose to keep, each backup now keeps one snapshot a week for 8 weeks and one a month for a year, and never prunes away its newest complete snapshot. Expect the backup drive to hold a little more.
- **Backups hold back when the source shrinks.** A run that finds less than half the files or bytes of the last snapshot (or nothing at all), as with a disk that isn't mounted or a folder moved away by mistake, makes no snapshot and notifies admins. If the change is expected, **Run anyway** on the backup's card makes one.
- **Re-uploading a folder asks first.** Dropping a folder, or files, that already exist asks once for the whole drop: replace same-named files (the old ones go to the trash), keep both by uploading into a numbered folder, or skip what's already there and upload only what's missing. Closing the tab while uploads are running asks first too.
- **Feels like an iPhone app on phones**, in Safari and installed to the Home Screen. A tab bar along the bottom (Home, Files, Shared, Search, More), a see-through title bar with a back button, and a large title that shrinks into the bar as you scroll. Tap a file to open it, or touch and hold it (or tap ⋯) for an action sheet with **Select** to pick several. Dialogs rise from the bottom as sheets you can swipe down to close, and they stay above the keyboard. Previews fill the screen, and you swipe left or right between files. The app runs under the status bar and clear of the Home indicator, the status bar follows your theme, fields no longer zoom the page when tapped, and notifications drop in from the top.
- **At home on iPad, and from the Home Screen.** Added to the Home Screen, Coffer opens on a launch screen in your theme's colour instead of a white flash, and the page no longer stretches past its ends behind the bars. On iPad, and on a phone held sideways, the sidebar, pages, previews, dialogs and notifications keep clear of the status bar, the Home indicator and the notch, and dialogs and the text editor stay above the on-screen keyboard. On touch screens small buttons take a finger without growing, the sidebar's rows and text fields are taller, a second tap never zooms the page, and search fields show a Search key and stop capitalising file names. A share page opened inside the installed app has a Back button, and previews there drop the *open in new tab* button, which had nowhere to open.
- **Watermarks are described as they work.** Images are watermarked on the server; videos, PDFs, documents and text are sent as they are, with the watermark drawn over them (documents and text now get it too). The link dialog and README say so.
- Every view uses the same page width. Folder pages show the folder name as the title with a small breadcrumb trail under it, and always show the folder's size and file count (also "0 B · 0 files"), so the header no longer jumps between folders.

### Removed

- **Mirrors.** Live folder copies onto another drive are gone, along with their admin page, the "Mirror again" action and the mirror status icons in folder listings. Backups stay and still keep dated snapshots. Mirrors set up on a development build are simply ignored, and files already copied stay where they are.
- **Back up photos.** The page in the account menu for copying a phone's photo library into a folder. Upload photos in Files as usual.
- **Photo albums and slideshows.** The Album link preset and the slideshow in the photo viewer. Folder and collection links can still open as a photo gallery (Layout → Photo gallery), with the full-screen viewer.
- **"Open on a phone" and "Receive from a phone".** These made short-lived network-only share links shown as a QR code. Every regular link still has a QR code, and a network allowlist can be set on any link. Links already made this way keep working until they expire.

### Fixed

- **Unplugged and unmounted disks show as offline.** Each local drive's folder now carries a small marker (`.coffer/drive-id`). A disk that isn't mounted, which leaves an empty folder in its place, shows the drive as offline instead of empty, so nothing is uploaded to the wrong disk and backups don't copy an empty folder. Coffer looks every minute, and before each backup and upload, and brings the drive back on its own when the disk returns (or straight away with **Rescan**). Existing drives get their marker the first time they're seen with files in them.
- Errors say what happened: "this drive is offline" and "the drive is full" instead of a generic server error.
- Backups that can't read a folder (missing permissions, a failing disk) say how many and which, notify admins, and don't count as a complete snapshot.
- Replacing a file by upload no longer loses the old version if the upload fails at the last step, and the old version in the trash shows its real size. Replaced files from share-link uploads now appear in the trash too.
- Emptying the trash says when items on an offline drive, or ones that couldn't be removed, were left, and the confirmation says when it includes files other people deleted in folders you share.
- WebDAV uploads stop before filling Coffer's own disk, keeping at least 2 GB (or 5%) free there.
- When locks can't be checked, changes are refused instead of allowed.
- Adding a user whose home folder can't be set up no longer leaves a half-made account.
- Temporary files left by a write cut short by a restart are cleaned up after a day.
- A drive whose folder or bucket couldn't be opened at start-up showed as online and failed on first use; it now shows as offline.
- The media worker keeps checking in while it renders a long video, so it no longer looks offline during big jobs.
- The backups list no longer reads a running backup's progress while the run is changing it.

### Security

- A path written with backslashes (`x\_coffer\…`) no longer gets a user who isn't an admin, or a link they make, into the `_coffer` copies of Coffer's database and key inside a backup. The check also ignores case now.
- Visitors of one link no longer see, or reply to, comments and approvals made through another link to the same file. They see their own link's threads and the team's comments that aren't internal.
- Upload links refuse files and folders the owner wouldn't see in listings (names starting with `.`, `_coffer`, `desktop.ini`…).
- Link passwords need at least 10 characters (links that already have a shorter one keep it), and each link takes fewer password guesses a minute.
- View-only and watermarked links only show pictures the server can watermark; SVG and EXR originals are no longer sent.
- Share links: download limits can no longer be skipped with range requests; "no downloads" links only serve previews; watermarked links also watermark thumbnails; email codes are capped at five tries even under parallel guesses; invite-only links drop people who only confirmed their own email; network-limited links refuse forwarded requests from untrusted proxies; internal review notes and guest emails stay private.
- Symlinks can no longer lead out of a granted folder or a shared folder (web app, WebDAV, share links and zips).
- Grants can't give more rights than the granter has, and lose rights when the granter does.
- WebDAV: a PROPPATCH after upload (Windows Explorer) or an interrupted PUT no longer empties or truncates files; locks are per user; `Depth: infinity` is refused; uploads stop at the quota or free space.
- Quotas also apply to moves, text edits, restores and parallel uploads.
- Sign-in: one 2FA code works once even under parallel sign-ins; sign-in can't undo a concurrent admin change; over HTTPS only the `__Host-` session cookie counts; `CF-Connecting-IP` is no longer trusted ahead of `X-Forwarded-For`.
- Downloads and WebDAV files that aren't media are sent as `application/octet-stream`, and Word previews no longer render embedded HTML.
- The media image ships a strict ImageMagick policy, and video renders never let ffmpeg guess the format; share visitors can no longer re-queue finished renders.
- Backup manifests are validated before restore, browse or prune.
- Local drives can't be `/proc`, `/sys`, `/dev` or contain the data folder; the data folder is 0700 and the databases 0600.
- Samba requires signing and only accepts private, VPN, link-local and loopback addresses by default (`SMB_HOSTS_ALLOW`).
- `coffer reset-password --admin <user>` restores an admin when none is left.
- Link visitors can no longer flood the owner: download and upload notifications are grouped into one per link every 10 minutes (with a count of the rest), each link's emails are capped per hour, and one address can get three verification codes an hour. A `HEAD` request no longer counts as a download.
- Previews through a link, including the video player's range requests, no longer use up its download limit. Only real downloads (and zips) count, once each.
- Hidden files inside a shared folder (such as `.env` or `.git`) can no longer be opened, browsed or zipped through the link, whatever the admin's "Hide hidden and system files" setting.
- Uploads that are still open now count against free space and storage limits by their full size, and each link, and each visitor's address, can only have 10 open at once.
- A zip download that fails half-way is now cut off, so it can't look complete, and a file deleted while zipping is left out instead of added empty.
- Forwarding headers are only trusted on port 8081, and only from `COFFER_TRUSTED_PROXIES`. The tunnel in `docker-compose.yml` now has a fixed address that Coffer trusts, so `COFFER_TRUSTED_PROXIES` no longer needs Docker's whole subnet (which also trusted the network's gateway); remove it from `.env` if you set it that way. A reverse proxy in front of 8080 needs the new `COFFER_APP_TRUSTED_PROXIES`.
- Saving over a file from WebDAV or the text editor needs permission to delete there, as replacing a file by upload does, and the old version goes to the trash first.
- Setting up two-factor asks for your password. Changing your password revokes your app passwords (and an admin setting a new one revokes that person's); turning on two-factor keeps them but lists them, so you can remove any you don't recognise.
- Image previews refuse images over 50 megapixels (24 for 16-bit images), watermarked previews are kept on disk for a day or two instead of being redrawn on every view, and the `coffer` container is limited to 2 GB of memory.
- `.env` files other than `.env.example` are kept out of git and Docker builds.
- The copy of Coffer's database and key inside a backup (`_coffer/`) now counts as hidden: share links never serve or zip it, and home and shared folders can't open it. A backup whose folder a non-admin can open no longer gets the copy at all, and admins are told why.
- A dropped folder can't be uploaded through a symlink that leads out of the folder it's uploaded into (web app and upload links).
- `coffer reset-password` also revokes the user's app passwords, as a reset in the app does.
- Markdown previews no longer render `<style>` or form controls from the document, so a file can't restyle the app around it.
- Samba refuses to start with the example password from `users.conf.example`.
- Desktop app: only the bundled connect page is treated as local on each platform.

## [0.3.0] - 2026-10-25

The first public release.

### Files

- Browse, search, preview and edit files from any browser, the installable web app, or the desktop apps for macOS and Linux.
- Multiple drives: local folders and S3-compatible buckets (Cloudflare R2, AWS S3, Backblaze B2, MinIO), with the same browsing, previews and sharing.
- Instant search across every drive, and recursive folder sizes with a storage breakdown by type.
- WebDAV at `/dav/` with per-device app passwords, and a hardened Samba share of the same files for fast work from desktop apps on the LAN.
- Previews for Word, Excel, CSV, ODS, PowerPoint, PDF, images, video and audio, and a text, code, Markdown and CSV editor.
- A media worker (`coffer-media` image) that makes browser-playable copies of ProRes, HEVC, MXF and other video, video thumbnails, and HEIC and camera RAW photo previews.
- Version stacks: `spot_v11.mov`, `spot_v12.mov`… show as one row.
- Locks (including files open over SMB), favourites, shared tags and a per-folder activity feed.

### Sharing

- Share by email without accounts: anyone with the link, anyone who confirms their email with a one-time code, or only invited people. See who opened and downloaded what, and remove one person at a time.
- Send files in one step, request files into a drop box, collections, photo albums, watermarks, QR codes and LAN-only links.
- Review and approval: comments at a video timecode or a spot on an image, approve or request changes, internal team notes.
- Links that always show the newest version of a file.
- Link presets and options: permissions, password, expiry, download limit, network allowlist, pause, upload rules and owner notifications.
- Share links run on their own listener (port 8081) with no sign-in, admin or WebDAV, ready for a Cloudflare Tunnel (`docker compose --profile tunnel`).

### Running it

- Email over SMTP for invitations, codes and notifications.
- Nightly deduplicated backups to another drive, with point-in-time restore.
- Storage limits per person, and a "Back up photos" page for phones.
- Users, folder grants, TOTP two-factor with recovery codes and an optional require-2FA policy.
- Monitoring dashboard, Prometheus `/metrics`, audit log, and an in-app notice when a new release is out.
- Images for x86-64 and ARM64 on GitHub Container Registry: `ghcr.io/oddomens/coffer`, `coffer-media` and `coffer-samba`.

[Unreleased]: https://github.com/OddOmens/Coffer/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/OddOmens/Coffer/releases/tag/v0.4.0
