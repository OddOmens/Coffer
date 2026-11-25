import { useSyncExternalStore } from "react"

/**
 * Resumable chunked uploads. Each file is created on the server, sent in
 * chunks with an Upload-Offset header, then completed. A failed chunk is
 * retried from the offset the server reports, so flaky connections and huge
 * renders both work.
 */

export type UploadStatus = "queued" | "uploading" | "done" | "error" | "canceled"

export type UploadItem = {
  key: number
  file: File
  name: string // relative name, may include folders
  dir: string
  status: UploadStatus
  loaded: number
  error?: string
  serverId?: string
  base: string
  place?: string
  uploader?: string
  overwrite: boolean
  /** Leave the file alone if it's already there (filling in a folder). */
  skipExisting: boolean
  /** It was already there, so nothing was sent. */
  skipped?: boolean
  onDone?: (dir: string) => void
  xhr?: XMLHttpRequest
}

const MAX_PARALLEL = 3
const MAX_RETRIES = 6

let items: UploadItem[] = []
let nextKey = 1
const listeners = new Set<() => void>()

function emit() {
  items = [...items]
  listeners.forEach((l) => l())
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

export function useUploads() {
  return useSyncExternalStore(subscribe, () => items)
}

export type QueueFile = { file: File; name: string }

export function enqueue(
  files: QueueFile[],
  opts: { dir: string; base: string; place?: string; uploader?: string; overwrite?: boolean; skipExisting?: boolean; onDone?: (dir: string) => void },
): number[] {
  const keys: number[] = []
  for (const f of files) {
    keys.push(nextKey)
    items.push({
      key: nextKey++,
      file: f.file,
      name: f.name,
      dir: opts.dir,
      status: "queued",
      loaded: 0,
      base: opts.base,
      place: opts.place,
      uploader: opts.uploader,
      overwrite: opts.overwrite ?? false,
      skipExisting: opts.skipExisting ?? false,
      onDone: opts.onDone,
    })
  }
  emit()
  pump()
  return keys
}

export function cancel(key: number) {
  const it = items.find((i) => i.key === key)
  if (!it || it.status === "done") return
  it.status = "canceled"
  it.xhr?.abort()
  if (it.serverId) {
    fetch(`${it.base}/${it.serverId}`, { method: "DELETE", credentials: "same-origin" }).catch(() => {})
  }
  emit()
  pump()
}

export function retry(key: number) {
  const it = items.find((i) => i.key === key)
  if (!it || it.status !== "error") return
  it.status = "queued"
  it.error = undefined
  emit()
  pump()
}

export function clearFinished() {
  items = items.filter((i) => i.status === "queued" || i.status === "uploading")
  emit()
}

// Leaving the page stops uploads; ask first.
if (typeof window !== "undefined") {
  window.addEventListener("beforeunload", (e) => {
    if (items.some((i) => i.status === "queued" || i.status === "uploading")) e.preventDefault()
  })
}

function pump() {
  const active = items.filter((i) => i.status === "uploading").length
  const next = items.filter((i) => i.status === "queued").slice(0, Math.max(0, MAX_PARALLEL - active))
  for (const it of next) {
    it.status = "uploading"
    run(it).finally(pump)
  }
  if (next.length) emit()
}

async function json<T>(res: Response): Promise<T> {
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw Object.assign(new Error(data.error ?? res.statusText), { status: res.status, data })
  return data as T
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

// A function call, so TypeScript does not narrow status across awaits.
const isCanceled = (it: UploadItem) => it.status === "canceled"

async function run(it: UploadItem) {
  try {
    let chunkSize = 16 << 20
    if (!it.serverId) {
      const res = await fetch(it.base, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          place: it.place,
          dir: it.dir,
          name: it.name,
          size: it.file.size,
          overwrite: it.overwrite,
          uploader: it.uploader,
          ...(it.skipExisting && { skipExisting: true }),
        }),
      })
      const created = await json<{ id: string; chunkSize: number; skipped?: boolean }>(res)
      if (created.skipped) {
        it.status = "done"
        it.skipped = true
        it.loaded = it.file.size
        emit()
        it.onDone?.(it.dir)
        return
      }
      it.serverId = created.id
      chunkSize = created.chunkSize || chunkSize
    } else {
      const res = await fetch(`${it.base}/${it.serverId}`, { credentials: "same-origin" })
      it.loaded = (await json<{ offset: number }>(res)).offset
    }

    let offset = it.loaded
    let failures = 0
    while (offset < it.file.size) {
      if (isCanceled(it)) return
      const end = Math.min(offset + chunkSize, it.file.size)
      try {
        offset = await sendChunk(it, offset, it.file.slice(offset, end))
        failures = 0
      } catch (err) {
        if (isCanceled(it)) return
        const e = err as { status?: number; offset?: number }
        if (e.status === 409 && typeof e.offset === "number") {
          offset = e.offset // server tells us where to resume
          continue
        }
        if (e.status && e.status >= 400 && e.status < 500 && e.status !== 408 && e.status !== 429) throw err
        if (++failures > MAX_RETRIES) throw err
        await sleep(Math.min(30000, 1000 * 2 ** failures))
      }
      it.loaded = offset
      emit()
    }
    const res = await fetch(`${it.base}/${it.serverId}/complete`, { method: "POST", credentials: "same-origin" })
    await json(res)
    it.status = "done"
    it.loaded = it.file.size
    emit()
    it.onDone?.(it.dir)
  } catch (err) {
    if (isCanceled(it)) return
    it.status = "error"
    it.error = (err as Error).message || "upload failed"
    emit()
  }
}

function sendChunk(it: UploadItem, offset: number, blob: Blob): Promise<number> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    it.xhr = xhr
    xhr.open("PATCH", `${it.base}/${it.serverId}`)
    xhr.setRequestHeader("Upload-Offset", String(offset))
    xhr.setRequestHeader("Content-Type", "application/octet-stream")
    xhr.upload.onprogress = (ev) => {
      it.loaded = offset + ev.loaded
      emit()
    }
    xhr.onload = () => {
      let data: { offset?: number; error?: string } = {}
      try {
        data = JSON.parse(xhr.responseText)
      } catch {
        /* empty */
      }
      if (xhr.status === 200 && typeof data.offset === "number") resolve(data.offset)
      else reject(Object.assign(new Error(data.error ?? `upload failed (${xhr.status})`), { status: xhr.status, offset: data.offset }))
    }
    xhr.onerror = () => reject(new Error("network error"))
    xhr.onabort = () => reject(new Error("canceled"))
    xhr.send(blob)
  })
}

/** Collects files from a drop, walking dropped folders. */
export async function filesFromDrop(dt: DataTransfer): Promise<QueueFile[]> {
  const entries: FileSystemEntry[] = []
  for (const item of Array.from(dt.items)) {
    const e = item.kind === "file" ? item.webkitGetAsEntry() : null
    if (e) entries.push(e)
  }
  if (!entries.length) return Array.from(dt.files).map((file) => ({ file, name: file.name }))
  const out: QueueFile[] = []
  const walk = async (entry: FileSystemEntry, prefix: string): Promise<void> => {
    if (entry.isFile) {
      const file = await new Promise<File>((res, rej) => (entry as FileSystemFileEntry).file(res, rej))
      if (!isJunk(file.name)) out.push({ file, name: prefix + file.name })
    } else if (entry.isDirectory) {
      const reader = (entry as FileSystemDirectoryEntry).createReader()
      for (;;) {
        const batch = await new Promise<FileSystemEntry[]>((res, rej) => reader.readEntries(res, rej))
        if (!batch.length) break
        for (const child of batch) await walk(child, `${prefix}${entry.name}/`)
      }
    }
  }
  for (const e of entries) await walk(e, "")
  return out
}

/** Files from an <input type=file webkitdirectory> keep their folder path. */
export function filesFromInput(list: FileList): QueueFile[] {
  return Array.from(list)
    .filter((f) => !isJunk(f.name))
    .map((file) => ({ file, name: file.webkitRelativePath || file.name }))
}

const isJunk = (name: string) => name === ".DS_Store" || name === "Thumbs.db" || name.startsWith("._")
