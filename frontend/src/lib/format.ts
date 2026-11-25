import type { Entry, Kind } from "./api"

export function formatBytes(n: number) {
  if (!Number.isFinite(n) || n < 0) return "—"
  if (n < 1024) return `${n} B`
  const units = ["KB", "MB", "GB", "TB", "PB"]
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

export const formatCount = (n: number) => new Intl.NumberFormat().format(n)

const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" })
const dayFmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" })

export const formatDate = (unix: number) => (unix ? dateFmt.format(unix * 1000) : "—")
export const formatDay = (unix: number) => (unix ? dayFmt.format(unix * 1000) : "—")

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" })
export function relativeTime(unix: number) {
  if (!unix) return "never"
  const diff = unix - Date.now() / 1000
  const abs = Math.abs(diff)
  const steps: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "second"],
    [3600, "minute"],
    [86400, "hour"],
    [86400 * 30, "day"],
    [86400 * 365, "month"],
  ]
  let prev = 1
  for (const [limit, unit] of steps) {
    if (abs < limit) return rtf.format(Math.round(diff / prev), unit)
    prev = limit
  }
  return rtf.format(Math.round(diff / (86400 * 365)), "year")
}

export function formatDuration(sec: number) {
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`
}

export function parentPath(p: string) {
  const i = p.lastIndexOf("/")
  return i <= 0 ? "/" : p.slice(0, i)
}

export function baseName(p: string) {
  return p.slice(p.lastIndexOf("/") + 1) || "/"
}

export const extOf = (name: string) => {
  const i = name.lastIndexOf(".")
  return i > 0 ? name.slice(i + 1).toLowerCase() : ""
}

/** Encodes a "/a/b c" path for use in a client-side route. */
export function encodePath(p: string) {
  return p.split("/").map((s) => encodeURIComponent(s)).join("/")
}

export const filesUrl = (place: string, path = "/") => `/files/${encodeURIComponent(place)}${path === "/" ? "" : encodePath(path)}`

const kindByExt: Record<string, Kind> = {}
const kindLists: [Kind, string[]][] = [
  ["image", ["jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "heic", "tif", "tiff", "bmp", "exr", "raw", "cr2", "nef", "dng"]],
  ["video", ["mp4", "mov", "m4v", "webm", "mkv", "avi", "mxf", "r3d", "braw"]],
  ["audio", ["mp3", "wav", "aif", "aiff", "flac", "m4a", "aac", "ogg"]],
  ["pdf", ["pdf"]],
  ["document", ["doc", "docx", "odt", "rtf", "pages", "gdoc"]],
  ["spreadsheet", ["xls", "xlsx", "ods", "csv", "numbers", "gsheet"]],
  ["presentation", ["ppt", "pptx", "odp", "key", "gslides"]],
  ["text", ["txt", "md", "log", "srt", "vtt"]],
  ["code", ["json", "xml", "yml", "yaml", "toml", "ini", "js", "jsx", "ts", "tsx", "css", "html", "htm", "py", "sh", "go", "rs", "sql", "jsxbin"]],
  ["archive", ["zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz"]],
  ["design", ["aep", "aet", "prproj", "psd", "ai", "indd", "fig", "sketch", "blend", "c4d", "mogrt", "xd"]],
  ["font", ["ttf", "otf", "woff", "woff2"]],
]
for (const [k, exts] of kindLists) for (const e of exts) kindByExt[e] = k

export function fileKind(e: Pick<Entry, "name" | "isDir"> & { kind?: Kind }): Kind {
  if (e.isDir) return "folder"
  return e.kind ?? kindByExt[extOf(e.name)] ?? "other"
}

/** Filter tabs in the explorer. */
export const kindFilters: { id: string; label: string; kinds: Kind[] }[] = [
  { id: "all", label: "View all", kinds: [] },
  { id: "docs", label: "Documents", kinds: ["document", "text", "pdf"] },
  { id: "sheets", label: "Spreadsheets", kinds: ["spreadsheet"] },
  { id: "slides", label: "Presentations", kinds: ["presentation"] },
  { id: "images", label: "Images", kinds: ["image"] },
  { id: "video", label: "Video", kinds: ["video"] },
  { id: "audio", label: "Audio", kinds: ["audio"] },
  { id: "design", label: "Projects", kinds: ["design"] },
]

export type PreviewKind = "image" | "video" | "audio" | "pdf" | "text" | "docx" | "sheet" | "pptx" | "google" | null

export function previewKind(e: Pick<Entry, "name" | "isDir">): PreviewKind {
  if (e.isDir) return null
  const x = extOf(e.name)
  // Photos and videos browsers can't show play from renditions the media worker makes.
  if (["jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "heic", "heif", "tif", "tiff", "bmp", "psd", "dng", "cr2", "cr3", "nef", "arw", "raf", "orf", "rw2", "pef", "srw", "3fr"].includes(x)) return "image"
  if (["mp4", "mov", "m4v", "webm", "mkv", "avi", "mxf", "mts", "m2ts", "wmv", "flv", "mpg", "mpeg", "3gp"].includes(x)) return "video"
  if (["mp3", "wav", "flac", "m4a", "aac", "ogg"].includes(x)) return "audio"
  if (x === "pdf") return "pdf"
  if (x === "docx") return "docx"
  if (["xlsx", "xls", "ods", "csv", "numbers"].includes(x)) return "sheet"
  if (x === "pptx") return "pptx"
  if (["gdoc", "gsheet", "gslides"].includes(x)) return "google"
  if (isEditable(e)) return "text"
  return null
}

const editableExts = new Set([
  "txt", "md", "markdown", "log", "srt", "vtt", "json", "xml", "yml", "yaml", "toml", "ini", "conf", "env", "js", "jsx",
  "ts", "tsx", "css", "scss", "html", "htm", "py", "sh", "go", "rs", "sql", "csv", "tsv", "jsxbin", "svg", "gitignore",
])

export function isEditable(e: Pick<Entry, "name" | "isDir">) {
  if (e.isDir) return false
  const x = extOf(e.name)
  return editableExts.has(x) || (x === "" && !e.name.includes("."))
}

const renditionThumbs = new Set([
  "heic", "heif", "avif", "tif", "tiff", "bmp", "psd", "dng", "cr2", "cr3", "nef", "arw", "raf", "orf", "rw2", "pef", "srw", "3fr",
  "mov", "mp4", "m4v", "webm", "mkv", "avi", "mxf", "mts", "m2ts", "wmv", "flv", "mpg", "mpeg", "3gp",
])

/** Thumbnails: rendered directly for common images, from the media worker's stills for photos and video. */
export const hasThumb = (e: Entry) =>
  !e.isDir && ((["jpg", "jpeg", "png", "gif", "webp"].includes(extOf(e.name)) && e.size < 80 << 20) || renditionThumbs.has(extOf(e.name)))

export const fileCount = (n: number) => `${n.toLocaleString()} file${n === 1 ? "" : "s"}`

export function describeEntry(e: Entry) {
  if (e.isDir) {
    if (e.size < 0) return "Folder"
    if (!e.items) return "Empty"
    return `${formatBytes(e.size)} · ${formatCount(e.items)} file${e.items === 1 ? "" : "s"}`
  }
  const x = extOf(e.name)
  return x ? `${formatBytes(e.size)} · ${x.toUpperCase()}` : formatBytes(e.size)
}

const kindNames: Record<string, string> = {
  folder: "Folder", image: "Image", video: "Video", audio: "Audio", pdf: "PDF", document: "Document",
  spreadsheet: "Spreadsheet", presentation: "Presentation", text: "Text", code: "Code", archive: "Archive",
  design: "Project file", font: "Font", other: "File",
}
const extNames: Record<string, string> = {
  docx: "Word document", doc: "Word document", xlsx: "Excel spreadsheet", xls: "Excel spreadsheet",
  pptx: "PowerPoint", ppt: "PowerPoint", aep: "After Effects project", prproj: "Premiere project",
  psd: "Photoshop file", ai: "Illustrator file", mov: "QuickTime movie", md: "Markdown", csv: "CSV",
  gdoc: "Google Doc", gsheet: "Google Sheet", gslides: "Google Slides", key: "Keynote", pages: "Pages", numbers: "Numbers",
}
export function kindLabel(e: Pick<Entry, "name" | "isDir"> & { kind?: Kind }) {
  if (e.isDir) return "Folder"
  return extNames[extOf(e.name)] ?? kindNames[fileKind(e)] ?? "File"
}
