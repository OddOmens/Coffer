import { forgetShell } from "@/lib/shell-cache"

export type Kind =
  | "folder" | "image" | "video" | "audio" | "pdf" | "document" | "spreadsheet" | "presentation"
  | "text" | "code" | "archive" | "design" | "font" | "other"

export type Entry = {
  name: string
  path: string
  isDir: boolean
  size: number // folders: recursive size from the index, -1 when not indexed yet
  items?: number // folders: files below
  modTime: number
  mime?: string
  kind: Kind
  symlink?: boolean
  // Decorations on folder listings.
  favorite?: boolean
  tags?: string[]
  lock?: Lock
  review?: ReviewSummary
  backup?: BackupMark
}

// Whether a backup holds this file or folder as it is now. done: yes;
// pending: covered, but new or changed since the last run; partial: a folder
// with backed-up folders inside; unknown: the snapshot couldn't be checked.
export type BackupMark = {
  state: "done" | "pending" | "partial" | "unknown"
  backup: string
  drive: string
  cloud: boolean
  encrypted?: boolean
  last?: number
}

export type Lock = { userId: number; username: string; note: string; createdAt: number; source: "coffer" | "smb" }

export type ReviewSummary = { comments: number; open: number; status?: "approved" | "changes"; by?: string; at?: number }

export type Comment = {
  id: number
  parentId: number
  author: string
  email?: string
  kind: "comment" | "approved" | "changes"
  body: string
  timecode: number | null
  x: number | null
  y: number | null
  resolved: boolean
  internal: boolean
  createdAt: number
  guest: boolean
}

export type CommentInput = {
  path: string
  body?: string
  kind?: Comment["kind"]
  timecode?: number | null
  x?: number | null
  y?: number | null
  parentId?: number
  internal?: boolean
  name?: string
}

/** A file or folder somewhere in the viewer's places (favorites, tags, collections). */
export type Item = {
  place: string
  placeName: string
  path: string
  name: string
  isDir: boolean
  size: number
  modTime: number
  kind: Kind
  missing?: boolean
}

export type Collection = {
  id: string
  name: string
  description: string
  createdAt: number
  updatedAt: number
  items: Item[]
  links: number
}

export type Tag = { tag: string; color: string; count: number }

export type Activity = { id: number; ts: number; who: string; guest: boolean; action: string; path: string; name: string; detail: string }

export type Recipient = {
  id: string
  email: string
  name: string
  createdAt: number
  invitedAt: number
  lastSeen: number
  views: number
  downloads: number
  revoked: boolean
}

export type MediaState = { state: "ready" | "queued" | "running" | "failed" | "unavailable"; error?: string }

export type Backup = {
  id: string
  name: string
  sourceDrive: string
  sourcePath: string
  /** Drive paths of the files and folders picked; empty for all of sourcePath. */
  items: string[]
  targetDrive: string
  targetPath: string
  hour: number
  keep: number
  enabled: boolean
  createdAt: number
  lastRun?: BackupRun
  running?: BackupRun
  nextRun: number
  sourceName: string
  targetName: string
  targetKind: string
  /** The last run was held back because the source shrank a lot. */
  canForce: boolean
}

export type BackupRun = {
  id: number
  startedAt: number
  finishedAt: number
  state: "running" | "done" | "failed"
  files: number
  bytes: number
  uploaded: number
  uploadedBytes: number
  snapshot: string
  error: string
}

export type Snapshot = { name: string; created: number }

export type SnapshotEntry = { name: string; path: string; isDir: boolean; size: number; modTime: number; items?: number }

export type Hit = Entry & { place: string; placeName: string }

export type Place = {
  id: string
  name: string
  driveId: string
  driveName: string
  driveKind: "local" | "s3"
  encrypted?: boolean
  kind: "drive" | "home" | "shared"
  canWrite: boolean
  canDelete: boolean
  canShare: boolean
  grantedBy?: string
  online: boolean
  root: string
  size: number
  items: number
  free: number
  total: number
}

export type Listing = {
  path: string
  entries: Entry[]
  place: Place
  size: number
  items: number
  backup?: BackupMark
  /** Set inside a backup's own folder, which only holds coded object names. */
  backupStore?: { id: string; name: string } | null
}

export type Prefs = {
  theme?: "light" | "dark" | "system"
  density?: "comfortable" | "compact"
  accent?: string
  view?: "list" | "grid"
  quickCreate?: boolean
  pageSize?: number
  stackVersions?: boolean
  emailNotify?: boolean
  showHidden?: boolean
}

export type Me = {
  id: number
  username: string
  role: "admin" | "user"
  totpEnabled: boolean
  disabled: boolean
  createdAt: number
  updatedAt: number
  mustEnroll2fa: boolean
  recoveryLeft: number
  prefs: Prefs
  email: string
  quotaBytes: number
  quotaUsed: number
  mailReady: boolean
}

export type AppConfig = {
  appName: string
  accentColor: string
  loginMessage: string
  hasLogo: boolean
  logoVersion: number
  publicOnly: boolean
  version: string
  defaultTheme: "light" | "dark" | "system"
  defaultDensity: "comfortable" | "compact"
  showQuickCreate: boolean
  webdav: boolean
}

export type Share = {
  id: string
  url: string
  name: string
  path: string
  place?: string
  driveName: string
  isDir: boolean
  hasPassword: boolean
  expiresAt: number
  maxDownloads: number
  downloads: number
  canView: boolean
  canDownload: boolean
  canUpload: boolean
  disabled: boolean
  title: string
  message: string
  note: string
  uploadMaxMB: number
  uploadExts: string
  uploadNamed: boolean
  notify: number
  allowedNets: string
  viewMode: ViewMode
  access: ShareAccess
  canComment: boolean
  latest: boolean
  watermark: boolean
  collection?: string
  recipients: Recipient[]
  owner: string
  createdAt: number
  lastAccess: number
  active: boolean
  missing: boolean
}

export type ViewMode = "list" | "grid" | "gallery"
/** link: anyone with it · email: anyone who confirms an address · invited: only invited addresses */
export type ShareAccess = "link" | "email" | "invited"

export type InviteInput = { emails: string[]; message?: string; request?: boolean }

export type ShareInput = Partial<{
  place: string
  path: string
  password: string
  expiresAt: number
  maxDownloads: number
  canView: boolean
  canDownload: boolean
  canUpload: boolean
  disabled: boolean
  title: string
  message: string
  note: string
  uploadMaxMB: number
  uploadExts: string
  uploadNamed: boolean
  notify: number
  allowedNets: string
  viewMode: ViewMode
  access: ShareAccess
  canComment: boolean
  latest: boolean
  watermark: boolean
  collection: string
  invite: InviteInput
  /** Made by Send files, so the folder can be cleared out after the link expires. */
  sent: boolean
}>

export const NOTIFY_OPEN = 1
export const NOTIFY_DOWNLOAD = 2
export const NOTIFY_UPLOAD = 4

export type Grant = {
  id: string
  userId: number
  username: string
  driveId: string
  path: string
  name: string
  canWrite: boolean
  canDelete: boolean
  canShare: boolean
  createdAt: number
}

export type Settings = {
  appName: string
  accentColor: string
  loginMessage: string
  logoType: string
  hideDotfiles: boolean
  shareRequirePassword: boolean
  shareDefaultExpiryDays: number
  shareMaxExpiryDays: number
  shareUploadMaxMB: number
  trashDays: number
  require2fa: "off" | "admins" | "all"
  webdav: boolean
  updateCheck: boolean
  scanMinutes: number
  scanMinutesS3: number
  defaultTheme: "light" | "dark" | "system"
  defaultDensity: "comfortable" | "compact"
  showQuickCreate: boolean
  smtpHost: string
  smtpPort: number
  smtpSecurity: "starttls" | "tls" | "none"
  smtpUser: string
  smtpFrom: string
  smtpFromName: string
  smtpHasPassword: boolean
  smtpPassword?: string // write only; "" clears it
  mailReady: boolean
  publicUrl: string
  shareFooter: string
  shareWebsite: string
  shareHidePowered: boolean
  mediaAuto: boolean
  proxyHeight: 720 | 1080
  healthEmail: boolean
  selfBackup: "auto" | "on" | "off"
  mediaCacheGB: number
}

export type TrashItem = {
  id: string
  name: string
  path: string
  place: string
  placeName: string
  isDir: boolean
  size: number
  deletedAt: number
  deletedBy: string
  /** Deleted by the signed-in user. */
  mine: boolean
}

export type Session = {
  id: string
  createdAt: number
  lastSeen: number
  expiresAt: number
  ip: string
  userAgent: string
  current: boolean
}

export type AppToken = { id: string; name: string; createdAt: number; lastUsed: number; lastIp: string }

export type Notification = {
  id: number
  kind: string
  title: string
  body: string
  link: string
  createdAt: number
  readAt: number
}

export type AuditEntry = {
  id: number
  ts: number
  userId: number
  username: string
  shareId: string
  ip: string
  action: string
  path: string
  detail: string
}

export type ScanInfo = {
  drive: string
  started: number
  finished: number
  files: number
  dirs: number
  bytes: number
  ms: number
  error: string
  running: boolean
}

export type S3Config = {
  endpoint: string
  region: string
  bucket: string
  prefix: string
  accessKey: string
  secretKey?: string
  insecure: boolean
  pathStyle: boolean
  directDownloads: boolean
  encrypt: boolean
}

export type Drive = {
  id: string
  name: string
  kind: "local" | "s3"
  sort: number
  disabled: boolean
  createdAt: number
  online: boolean
  error?: string
  path?: string
  s3?: S3Config
  free: number
  total: number
  scan: ScanInfo
  recoveryKey?: string // only right after an encrypted drive gets its key
}

export type Sample = {
  t: number
  requests: number
  errors: number
  bytesOut: number
  bytesIn: number
  cpu: number
  mem: number
  goroutines: number
  conns: number
  shareHits: number
  load1: number
}

export type UpdateStatus = {
  current: string
  latest?: { version: string; url: string; notes: string; publishedAt: string }
  available: boolean
  checkedAt: number
  error?: string
  enabled: boolean
}

export type MonitorData = {
  system: {
    uptime: number
    goVersion: string
    cpus: number
    mem: number
    heap: number
    goroutines: number
    conns: number
    load: [number, number, number]
    totals: Sample
    history: Sample[]
  }
  drives: Drive[]
  stats: Record<string, number>
  version: string
  update: UpdateStatus
  media: { workerOnline: boolean; jobs: Record<string, number> }
  mail: boolean
  health: HealthProblem[]
}

/** Something wrong on the server, from the health check every few minutes. */
export type HealthProblem = {
  key: string
  severity: "warning" | "critical"
  title: string
  detail?: string
  link: string
  since: number
}

export type BatchResult = { done: number; errors: { path: string; error: string }[] }

export type PublicShare = {
  name: string
  title: string
  message: string
  isDir: boolean
  needsPassword: boolean
  unlocked: boolean
  canView: boolean
  canDownload: boolean
  canUpload: boolean
  expiresAt: number
  uploadMaxMB: number
  uploadExts: string[] | null
  uploadNamed: boolean
  viewMode: ViewMode
  access: ShareAccess
  /** Why the content is hidden: "" when visible. */
  gate: "" | "share_email" | "share_revoked" | "share_locked"
  viewer?: { email: string; name: string; revoked: boolean }
  canComment: boolean
  watermark: boolean
  collection: boolean
  description?: string
  from: string
  mailReady: boolean
  footer: string
  website: string
  hidePowered: boolean
  version?: number
  size?: number
  items?: number
  modTime?: number
  mime?: string
  kind?: Kind
}

/** Entries from a share listing, with review state when comments are on. */
export type ShareEntry = Entry & { review?: ReviewSummary }

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

let onUnauthenticated: () => void = () => {}
export function setUnauthenticatedHandler(fn: () => void) {
  onUnauthenticated = fn
}

export async function request<T>(method: string, url: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, {
    method,
    credentials: "same-origin",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  })
  if (res.status === 204 || res.status === 202) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = new ApiError(res.status, data.code ?? "error", data.error ?? res.statusText)
    if (res.status === 401 && err.code === "unauthenticated") {
      forgetShell()
      onUnauthenticated()
    }
    throw err
  }
  return data as T
}

/** Reads a preview rendition's state; 202 responses carry it too. */
export async function mediaState(url: string): Promise<MediaState> {
  const res = await fetch(url + (url.includes("?") ? "&" : "?") + "status=1", { credentials: "same-origin" })
  const data = await res.json().catch(() => ({}))
  if (!res.ok && res.status !== 202) return { state: "failed", error: data.error ?? res.statusText }
  return data as MediaState
}

export const qs = (params: Record<string, string | string[] | number | undefined>) => {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue
    for (const item of Array.isArray(v) ? v : [v]) sp.append(k, String(item))
  }
  return sp.toString()
}

export const api = {
  config: () => request<AppConfig>("GET", "/api/config"),
  login: (username: string, password: string, code?: string) =>
    request<Me>("POST", "/api/auth/login", { username, password, code: code ?? "" }),
  logout: () => request<void>("POST", "/api/auth/logout").finally(forgetShell),
  me: () => request<Me>("GET", "/api/me"),
  setEmail: (email: string) => request<Me>("PUT", "/api/me/email", { email }),
  savePrefs: (p: Prefs) => request<Prefs>("PUT", "/api/me/prefs", p),
  changePassword: (current: string, next: string) =>
    request<{ appPasswordsRevoked: number }>("POST", "/api/me/password", { current, new: next }),
  totpSetup: (password: string) => request<{ secret: string; uri: string }>("POST", "/api/me/totp/setup", { password }),
  totpEnable: (code: string, password: string) =>
    request<{ me: Me; recoveryCodes: string[]; appPasswords: string[] }>("POST", "/api/me/totp/enable", { code, password }),
  totpDisable: (password: string) => request<Me>("POST", "/api/me/totp/disable", { password }),
  recoveryCodes: (password: string) => request<{ recoveryCodes: string[] }>("POST", "/api/me/recovery", { password }),
  sessions: () => request<Session[]>("GET", "/api/me/sessions"),
  revokeSession: (id: string) => request<void>("DELETE", `/api/me/sessions/${encodeURIComponent(id)}`),
  tokens: () => request<AppToken[]>("GET", "/api/me/tokens"),
  createToken: (name: string, password: string) =>
    request<{ token: AppToken; secret: string; username: string }>("POST", "/api/me/tokens", { name, password }),
  deleteToken: (id: string) => request<void>("DELETE", `/api/me/tokens/${id}`),
  notifications: () => request<{ items: Notification[]; unread: number }>("GET", "/api/notifications"),
  markRead: (ids?: number[]) => request<void>("POST", "/api/notifications/read", { ids: ids ?? [] }),

  places: () => request<{ places: Place[]; home: string }>("GET", "/api/places"),
  grant: (input: { place: string; path: string; users: number[]; canWrite: boolean; canDelete: boolean; canShare: boolean }) =>
    request<{ added: number }>("POST", "/api/places/share", input),
  grants: () => request<Grant[]>("GET", "/api/places/granted"),
  deletePlace: (id: string) => request<void>("DELETE", `/api/places/${id}`),
  directory: () => request<{ id: number; username: string }[]>("GET", "/api/users/directory"),

  list: (place: string, path: string) => request<Listing>("GET", `/api/files/list?${qs({ place, path })}`),
  search: (query: string, opts: { place?: string; path?: string; kinds?: string[]; limit?: number } = {}, signal?: AbortSignal) =>
    request<{ entries: Hit[]; ms: number }>(
      "GET",
      `/api/search?${qs({ q: query, place: opts.place, path: opts.place ? opts.path : undefined, kinds: opts.kinds?.join(","), limit: opts.limit })}`,
      undefined,
      signal,
    ),
  recent: (limit = 20) => request<{ entries: Hit[] }>("GET", `/api/recent?${qs({ limit })}`),
  breakdown: (place: string) =>
    request<{ kind: Kind; files: number; bytes: number }[]>("GET", `/api/files/breakdown?${qs({ place })}`),
  mkdir: (place: string, parent: string, name: string) => request<Entry>("POST", "/api/files/mkdir", { place, parent, name }),
  rename: (place: string, path: string, name: string) => request<{ path: string }>("POST", "/api/files/rename", { place, path, name }),
  move: (place: string, paths: string[], destPlace: string, dest: string) =>
    request<BatchResult>("POST", "/api/files/move", { place, paths, destPlace, dest }),
  copy: (place: string, paths: string[], destPlace: string, dest: string) =>
    request<BatchResult>("POST", "/api/files/copy", { place, paths, destPlace, dest }),
  remove: (place: string, paths: string[], permanent = false) =>
    request<BatchResult>("POST", "/api/files/delete", { place, paths, permanent }),
  readText: (place: string, path: string) =>
    request<{ content: string; etag: string; size: number; modTime: number; writable: boolean }>(
      "GET",
      `/api/files/text?${qs({ place, path })}`,
    ),
  writeText: (input: { place: string; path: string; content: string; etag?: string; create?: boolean }) =>
    request<{ etag: string; path: string; modTime: number; size: number }>("PUT", "/api/files/text", input),
  downloadUrl: (place: string, path: string, inline = false) =>
    `/api/files/download?${qs({ place, path, inline: inline ? "1" : undefined })}`,
  thumbUrl: (place: string, e: Entry) => `/api/files/thumb?${qs({ place, path: e.path, v: e.modTime })}`,
  zipUrl: (place: string, path: string, names?: string[]) => `/api/files/zip?${qs({ place, path, name: names })}`,
  mediaUrl: (place: string, e: Pick<Entry, "path" | "modTime">, kind: "proxy" | "still") =>
    `/api/files/media?${qs({ place, path: e.path, kind, v: e.modTime })}`,

  comments: (place: string, path: string) => request<Comment[]>("GET", `/api/files/comments?${qs({ place, path })}`),
  addComment: (place: string, input: CommentInput) => request<Comment>("POST", "/api/files/comments", { place, ...input }),
  resolveComment: (id: number, resolved: boolean) => request<Comment>("PATCH", `/api/comments/${id}`, { resolved }),
  deleteComment: (id: number) => request<void>("DELETE", `/api/comments/${id}`),
  activity: (place: string, path: string, before?: number) =>
    request<Activity[]>("GET", `/api/files/activity?${qs({ place, path, before })}`),
  lock: (place: string, path: string, note = "") => request<Lock>("POST", "/api/files/lock", { place, path, note }),
  unlock: (place: string, path: string) => request<void>("DELETE", `/api/files/lock?${qs({ place, path })}`),
  editTags: (place: string, paths: string[], add: string[], remove: string[]) =>
    request<void>("POST", "/api/files/tags", { place, paths, add, remove }),
  tags: () => request<Tag[]>("GET", "/api/tags"),
  tagged: (tag: string) => request<Item[]>("GET", `/api/tags/${encodeURIComponent(tag)}`),
  setTagColor: (tag: string, color: string) => request<void>("PUT", `/api/tags/${encodeURIComponent(tag)}/color`, { color }),
  favorites: () => request<Item[]>("GET", "/api/favorites"),
  setFavorite: (place: string, path: string, on: boolean) => request<void>("PUT", "/api/favorites", { place, path, on }),
  collections: () => request<Collection[]>("GET", "/api/collections"),
  createCollection: (input: { name: string; description?: string; place?: string; paths?: string[] }) =>
    request<Collection>("POST", "/api/collections", input),
  updateCollection: (id: string, input: { name?: string; description?: string }) =>
    request<Collection>("PATCH", `/api/collections/${id}`, input),
  deleteCollection: (id: string) => request<void>("DELETE", `/api/collections/${id}`),
  addToCollection: (id: string, place: string, paths: string[]) =>
    request<Collection>("POST", `/api/collections/${id}/items`, { place, paths }),
  removeFromCollection: (id: string, place: string, path: string) =>
    request<Collection>("POST", `/api/collections/${id}/items/remove`, { place, path }),

  trash: () => request<TrashItem[]>("GET", "/api/trash"),
  restore: (id: string) => request<{ path: string; place: string }>("POST", `/api/trash/${id}/restore`),
  purge: (id: string) => request<void>("DELETE", `/api/trash/${id}`),
  emptyTrash: () => request<{ deleted: number; offline: number; failed: number }>("DELETE", "/api/trash"),

  shares: (opts: { all?: boolean; place?: string; path?: string } = {}) =>
    request<Share[]>("GET", `/api/shares?${qs({ all: opts.all ? "1" : undefined, place: opts.place, path: opts.path })}`),
  createShare: (input: ShareInput) => request<Share>("POST", "/api/shares", input),
  updateShare: (id: string, input: ShareInput) => request<Share>("PATCH", `/api/shares/${id}`, input),
  deleteShare: (id: string) => request<void>("DELETE", `/api/shares/${id}`),
  invite: (id: string, input: InviteInput) => request<Share>("POST", `/api/shares/${id}/invite`, input),
  updateRecipient: (id: string, rid: string, input: { revoked: boolean }) =>
    request<Share>("PATCH", `/api/shares/${id}/recipients/${rid}`, input),

  users: () => request<(Me & { places: (Grant & { grantedById: number })[] })[]>("GET", "/api/admin/users"),
  createUser: (input: Record<string, unknown>) => request<Me>("POST", "/api/admin/users", input),
  updateUser: (id: number, input: Record<string, unknown>) => request<Me>("PATCH", `/api/admin/users/${id}`, input),
  deleteUser: (id: number) => request<void>("DELETE", `/api/admin/users/${id}`),
  userPlaces: (id: number) => request<(Grant & { driveName: string; grantedBy: string })[]>("GET", `/api/admin/users/${id}/places`),
  addUserPlace: (id: number, input: Record<string, unknown>) => request<Grant>("POST", `/api/admin/users/${id}/places`, input),
  updateUserPlace: (placeId: string, input: Record<string, unknown>) => request<Grant>("PUT", `/api/admin/places/${placeId}`, input),
  drives: () => request<{ drives: Drive[]; roots: string[] }>("GET", "/api/admin/drives"),
  createDrive: (input: Record<string, unknown>) => request<Drive>("POST", "/api/admin/drives", input),
  updateDrive: (id: string, input: Record<string, unknown>) => request<Drive>("PATCH", `/api/admin/drives/${id}`, input),
  deleteDrive: (id: string) => request<void>("DELETE", `/api/admin/drives/${id}`),
  testDrive: (input: Record<string, unknown>, id?: string) =>
    request<{ ok: boolean; error?: string }>("POST", `/api/admin/drives/test${id ? `?id=${id}` : ""}`, input),
  rescanDrive: (id: string) => request<void>("POST", `/api/admin/drives/${id}/rescan`),
  driveRecoveryKey: (id: string, password: string) =>
    request<{ recoveryKey: string }>("POST", `/api/admin/drives/${id}/recovery-key`, { password }),
  settings: () => request<Settings>("GET", "/api/admin/settings"),
  saveSettings: (s: Settings) => request<Settings>("PUT", "/api/admin/settings", s),
  testEmail: (to: string) => request<void>("POST", "/api/admin/email/test", { to }),
  backups: () => request<Backup[]>("GET", "/api/admin/backups"),
  createBackup: (input: Record<string, unknown>) => request<Backup>("POST", "/api/admin/backups", input),
  updateBackup: (id: string, input: Partial<Pick<Backup, "name" | "hour" | "keep" | "enabled" | "items">>) =>
    request<Backup>("PATCH", `/api/admin/backups/${id}`, input),
  /** Adds files and folders of a place to a backup (a new one without `backup`), or takes them out. */
  backupItems: (input: { place: string; paths: string[]; backup?: string; name?: string; targetDrive?: string; remove?: boolean; run?: boolean }) =>
    request<{ backup: Backup; added: number; already: number; removed: number; started: boolean }>("POST", "/api/admin/backups/items", input),
  deleteBackup: (id: string) => request<void>("DELETE", `/api/admin/backups/${id}`),
  runBackup: (id: string, force = false) => request<void>("POST", `/api/admin/backups/${id}/run${force ? "?force=1" : ""}`),
  backupRuns: (id: string) => request<BackupRun[]>("GET", `/api/admin/backups/${id}/runs`),
  snapshots: (id: string) => request<Snapshot[]>("GET", `/api/admin/backups/${id}/snapshots`),
  browseSnapshot: (id: string, snap: string, path: string) =>
    request<{ path: string; entries: SnapshotEntry[]; files: number }>(
      "GET",
      `/api/admin/backups/${id}/snapshots/${encodeURIComponent(snap)}?${qs({ path })}`,
    ),
  /** One file of a snapshot, read straight from the backup drive. */
  snapshotFileUrl: (id: string, snap: string, path: string, inline = false) =>
    `/api/admin/backups/${id}/snapshots/${encodeURIComponent(snap)}/file?${qs({ path, inline: inline ? "1" : undefined })}`,
  /** A zip of the named entries of a snapshot folder, or all of it. */
  snapshotZipUrl: (id: string, snap: string, path: string, names?: string[]) =>
    `/api/admin/backups/${id}/snapshots/${encodeURIComponent(snap)}/zip?${qs({ path, name: names })}`,
  restoreBackup: async (id: string, snapshot: string, paths: string[]) => {
    const res = await fetch(`/api/admin/backups/${id}/restore`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ snapshot, paths }),
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new ApiError(res.status, data.code ?? "error", data.error ?? res.statusText)
    return data as { into: string; drive: string }
  },
  uploadLogo: async (file: File) => {
    const res = await fetch("/api/admin/logo", { method: "PUT", body: file, credentials: "same-origin" })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new ApiError(res.status, data.code ?? "error", data.error ?? "upload failed")
    }
  },
  deleteLogo: () => request<void>("DELETE", "/api/admin/logo"),
  audit: (before?: number, action?: string) =>
    request<AuditEntry[]>("GET", `/api/admin/audit?${qs({ before, action, limit: 100 })}`),
  monitor: () => request<MonitorData>("GET", "/api/admin/monitor"),
  health: () => request<{ problems: HealthProblem[] }>("GET", "/api/admin/health"),
  checkUpdates: () => request<UpdateStatus>("POST", "/api/admin/updates/check"),
}

/** Public share API, scoped to one token. */
export function shareApi(token: string) {
  const base = `/api/s/${encodeURIComponent(token)}`
  return {
    meta: () => request<PublicShare>("GET", base),
    auth: (password: string) => request<void>("POST", `${base}/auth`, { password }),
    verify: (email: string, name?: string) => request<void>("POST", `${base}/verify`, { email, name: name ?? "" }),
    verifyCheck: (email: string, code: string, name?: string) =>
      request<void>("POST", `${base}/verify/check`, { email, code, name: name ?? "" }),
    verifyInvite: (invite: string) => request<void>("POST", `${base}/verify/invite`, { invite }),
    forget: () => request<void>("POST", `${base}/forget`),
    list: (path: string) => request<{ path: string; entries: ShareEntry[] }>("GET", `${base}/list?${qs({ path })}`),
    comments: (path: string) => request<Comment[]>("GET", `${base}/comments?${qs({ path })}`),
    addComment: (input: CommentInput) => request<Comment>("POST", `${base}/comments`, input),
    mediaUrl: (e: Pick<Entry, "path" | "modTime">, kind: "proxy" | "still") => `${base}/media?${qs({ path: e.path, kind, v: e.modTime })}`,
    downloadUrl: (path: string, inline = false) => `${base}/download?${qs({ path, inline: inline ? "1" : undefined })}`,
    thumbUrl: (e: Entry) => `${base}/thumb?${qs({ path: e.path, v: e.modTime })}`,
    zipUrl: (path: string, names?: string[]) => `${base}/zip?${qs({ path, name: names })}`,
    uploadBase: `${base}/uploads`,
  }
}
