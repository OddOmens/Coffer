import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  CopyIcon,
  DatabaseBackupIcon,
  DownloadIcon,
  FilePenIcon,
  FilePlusIcon,
  FileUpIcon,
  FolderInputIcon,
  FolderOpenIcon,
  FolderPlusIcon,
  FolderUpIcon,
  HistoryIcon,
  LayoutGridIcon,
  LibraryIcon,
  Link2Icon,
  ListIcon,
  LockIcon,
  MessageSquareIcon,
  PencilIcon,
  PlusIcon,
  SearchIcon,
  SendIcon,
  SheetIcon,
  StarIcon,
  StarOffIcon,
  TagIcon,
  Trash2Icon,
  UnlockIcon,
  UploadCloudIcon,
  XIcon,
  type LucideIcon,
} from "lucide-react"
import { useNavigate, useParams, useSearchParams } from "react-router"
import { toast } from "sonner"

import { usePlaces, useUser } from "@/components/app-layout"
import { BackupDialog } from "@/components/backup-dialog"
import { ConflictDialog, DeleteDialog, FolderPickerDialog, NameDialog } from "@/components/dialogs"
import { ActionTile } from "@/components/file-icon"
import { BackupBadge, FileList, keyOf, sortEntries, useSelection, type Action, type Sort } from "@/components/file-list"
import { Page } from "@/components/page"
import { PreviewDialog } from "@/components/preview-dialog"
import { ActivitySheet, CollectionPickerDialog, TagsDialog, useTagColors } from "@/components/organize-dialogs"
import { SendDialog } from "@/components/send-dialog"
import { ShareDialog, type ShareTarget } from "@/components/share-dialog"
import { EmptyState, Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { api, ApiError, type BatchResult, type Entry, type Hit, type Place } from "@/lib/api"
import { baseName, encodePath, fileCount, filesUrl, formatBytes, isEditable, kindFilters, parentPath, previewKind } from "@/lib/format"
import { usePrefs } from "@/lib/prefs"
import { enqueue, filesFromDrop, filesFromInput, type QueueFile } from "@/lib/upload"
import { cn } from "@/lib/utils"
import { stackKey, stackVersions, versionOf, type Stacked } from "@/lib/versions"

type DialogState =
  | { type: "newFolder" }
  | { type: "newFile"; kind: "doc" | "sheet" }
  | { type: "rename"; entry: Entry }
  | { type: "delete"; entries: Entry[] }
  | { type: "move" | "copy"; entries: Entry[] }
  | { type: "share"; target: ShareTarget }
  | { type: "conflict"; files: QueueFile[]; names: string[]; folders: string[]; taken: string[]; dir: string }
  | { type: "tags"; entries: Entry[] }
  | { type: "collection"; entries: Entry[] }
  | { type: "backup"; entries: Entry[] }
  | { type: "send" }
  | { type: "activity" }
  | null

function download(url: string) {
  const a = document.createElement("a")
  a.href = url
  a.download = ""
  document.body.appendChild(a)
  a.click()
  a.remove()
}

export const editUrl = (place: string, path: string) => `/edit/${encodeURIComponent(place)}${encodePath(path)}`

function QuickCard({ icon, label, onClick }: { icon: LucideIcon; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="group flex min-w-[11rem] flex-1 items-center gap-3 rounded-xl border bg-card p-3 text-left transition-colors hover:border-ring/50"
    >
      <ActionTile icon={icon} />
      <span className="flex-1 truncate text-sm font-medium">{label}</span>
      <PlusIcon className="size-4 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground" />
    </button>
  )
}

export default function FilesPage() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const params = useParams()
  const [search, setSearch] = useSearchParams()
  const placeId = params.place ?? ""
  const path = ("/" + (params["*"] ?? "")).replace(/\/+$/, "") || "/"
  const [prefs, setPrefs] = usePrefs()
  const places = usePlaces()
  const me = useUser()
  const tagColor = useTagColors()
  const [stacksOpen, setStacksOpen] = useState<Set<string>>(new Set())

  const [sort, setSort] = useState<Sort>({ key: "name", dir: "asc" })
  const [kindTab, setKindTab] = useState("all")
  const [query, setQuery] = useState("")
  const [term, setTerm] = useState("")
  const searching = term.length > 0
  const [dialog, setDialog] = useState<DialogState>(null)
  const [preview, setPreview] = useState<number | null>(null)
  const [reviewOpen, setReviewOpen] = useState(false)
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const fileInput = useRef<HTMLInputElement>(null)
  const folderInput = useRef<HTMLInputElement>(null)

  useEffect(() => {
    setQuery("")
    setTerm("")
  }, [placeId, path])
  useEffect(() => {
    const t = setTimeout(() => setTerm(query.trim()), 120)
    return () => clearTimeout(t)
  }, [query])

  const listing = useQuery({
    queryKey: ["list", placeId, path, prefs.showHidden],
    queryFn: () => api.list(placeId, path),
  })
  // A backup's own folder (.backups/<name>) holds coded object names, so open
  // its snapshots instead: the same files by name, decrypted as you open them.
  const backupStore = listing.data?.backupStore
  useEffect(() => {
    if (backupStore) navigate(`/admin/backups/${backupStore.id}/browse`, { replace: true })
  }, [backupStore, navigate])
  const place: Place | undefined = listing.data?.place ?? places.data?.places.find((p) => p.id === placeId)
  const kinds = kindFilters.find((k) => k.id === kindTab)?.kinds ?? []
  const results = useQuery({
    queryKey: ["search", placeId, path, term, kindTab],
    queryFn: ({ signal }) => api.search(term, { place: placeId, path, kinds, limit: 300 }, signal),
    enabled: searching,
    placeholderData: (prev) => prev,
  })
  const shares = useQuery({ queryKey: ["shares"], queryFn: () => api.shares(), staleTime: 30_000 })
  const sharedPaths = useMemo(
    () => new Set((shares.data ?? []).filter((s) => s.active && s.place === placeId).map((s) => s.path)),
    [shares.data, placeId],
  )

  const entries: Stacked<Entry>[] = useMemo(() => {
    if (searching) return sortEntries(results.data?.entries ?? [], sort)
    const all = listing.data?.entries ?? []
    const filtered = kinds.length ? all.filter((e) => kinds.includes(e.kind)) : all
    const sorted = sortEntries(filtered, sort)
    return prefs.stackVersions ? stackVersions(sorted, stacksOpen) : sorted
  }, [searching, results.data, listing.data, sort, kinds, prefs.stackVersions, stacksOpen])
  const selection = useSelection(entries)
  const files = useMemo(() => entries.filter((e) => !e.isDir), [entries])

  const refresh = useCallback(() => {
    qc.invalidateQueries({ queryKey: ["list"] })
    qc.invalidateQueries({ queryKey: ["search"] })
    qc.invalidateQueries({ queryKey: ["places"] })
    qc.invalidateQueries({ queryKey: ["recent"] })
  }, [qc])

  const go = (p: string) => navigate(filesUrl(placeId, p))
  const canWrite = !!place?.canWrite
  const canDelete = !!place?.canDelete
  const canBackup = me.role === "admin" && !!place?.online

  const open = useCallback(
    (e: Entry) => {
      if (e.isDir) return navigate(filesUrl(placeId, e.path))
      if (previewKind(e)) setPreview(files.findIndex((x) => x.path === e.path))
      else download(api.downloadUrl(placeId, e.path))
    },
    [files, navigate, placeId],
  )

  // ?open=name opens a file's preview (from search or notifications).
  useEffect(() => {
    const name = search.get("open")
    if (!name || !listing.data) return
    const e = listing.data.entries.find((x) => x.name === name)
    if (e) {
      setPreview(null)
      if (previewKind(e)) setPreview(files.findIndex((x) => x.path === e.path))
      else selection.selectOnly(keyOf(e))
    }
    search.delete("open")
    setSearch(search, { replace: true })
  }, [search, listing.data]) // eslint-disable-line react-hooks/exhaustive-deps

  const downloadEntries = (list: Entry[]) => {
    if (list.length === 1 && !list[0].isDir) return download(api.downloadUrl(placeId, list[0].path))
    const dir = parentPath(list[0].path)
    if (list.some((e) => parentPath(e.path) !== dir)) {
      list.forEach((e) => download(e.isDir ? api.zipUrl(placeId, parentPath(e.path), [e.name]) : api.downloadUrl(placeId, e.path)))
      return
    }
    download(api.zipUrl(placeId, dir, list.map((e) => e.name)))
  }

  const report = (r: BatchResult, verb: string) => {
    if (r.errors.length) toast.error(`${r.errors.length} item${r.errors.length === 1 ? "" : "s"} could not be ${verb}: ${r.errors[0].error}`)
    else if (r.done) toast.success(`${r.done} item${r.done === 1 ? "" : "s"} ${verb}`)
    selection.clear()
    refresh()
    qc.invalidateQueries({ queryKey: ["shares"] })
  }

  const move = async (paths: string[], dest: string) => {
    try {
      report(await api.move(placeId, paths, placeId, dest), "moved")
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  const startUpload = (queue: QueueFile[], dir: string) => {
    if (!queue.length) return
    if (!canWrite) return toast.error("You have read-only access here")
    const taken = dir === path ? (listing.data?.entries ?? []).map((e) => e.name) : []
    const existing = new Set(taken)
    // Files and dropped folders that are already here: ask once for all.
    const clashes = [...new Set(queue.map((f) => topName(f.name)))].filter((n) => existing.has(n))
    if (clashes.length) {
      const folders = clashes.filter((n) => queue.some((f) => f.name.startsWith(n + "/")))
      return setDialog({ type: "conflict", files: queue, names: clashes, folders, taken, dir })
    }
    enqueue(queue, { dir, base: "/api/uploads", place: placeId, onDone: refresh })
  }

  const resolveConflict = (choice: "replace" | "keep" | "skip") => {
    if (dialog?.type !== "conflict") return
    const { files: queue, names, folders, taken, dir } = dialog
    setDialog(null)
    const clash = new Set(names)
    const rest = queue.filter((f) => !clash.has(topName(f.name)))
    const clashing = queue.filter((f) => clash.has(topName(f.name)))
    const opts = { dir, base: "/api/uploads", place: placeId, onDone: refresh }
    if (rest.length) enqueue(rest, opts)
    if (!clashing.length) return
    if (choice === "replace") {
      // Same-named files are replaced; the old ones go to the trash.
      enqueue(clashing, { ...opts, overwrite: true })
    } else if (choice === "skip") {
      // Single files are left out; folders get only what they're missing.
      const inFolders = clashing.filter((f) => f.name.includes("/"))
      if (inFolders.length) enqueue(inFolders, { ...opts, skipExisting: true })
    } else {
      // A folder goes in as "Name (2)"; a file gets a numbered name.
      const used = new Set(taken)
      const renamed = new Map<string, string>()
      for (const n of folders) {
        let i = 2
        while (used.has(`${n} (${i})`)) i++
        used.add(`${n} (${i})`)
        renamed.set(n, `${n} (${i})`)
      }
      enqueue(
        clashing.map((f) => {
          const top = topName(f.name)
          const to = renamed.get(top)
          return to && f.name.length > top.length ? { ...f, name: to + f.name.slice(top.length) } : f
        }),
        opts,
      )
    }
  }

  const shareTarget = (e: Entry): ShareTarget => ({ place: placeId, path: e.path, name: e.name, isDir: e.isDir, versioned: !!versionOf(e.name) })

  const toggleFavorite = async (list: Entry[], on: boolean) => {
    try {
      await Promise.all(list.map((e) => api.setFavorite(placeId, e.path, on)))
      refresh()
      qc.invalidateQueries({ queryKey: ["favorites"] })
    } catch (err) {
      toast.error((err as Error).message)
    }
  }

  const setLock = async (e: Entry, on: boolean) => {
    try {
      if (on) {
        await api.lock(placeId, e.path)
        toast.success(`Locked “${e.name}”. Others can't change it in Coffer until you unlock it.`)
      } else {
        await api.unlock(placeId, e.path)
        toast.success(`Unlocked “${e.name}”`)
      }
      refresh()
    } catch (err) {
      toast.error((err as Error).message)
    }
  }

  const actionsFor = (e: Entry): Action[] => {
    const sel = selection.selected.has(keyOf(e)) ? selection.items : [e]
    const multi = sel.length > 1
    const list: Action[] = []
    if (!multi) list.push({ label: e.isDir ? "Open" : "Preview", icon: <FolderOpenIcon />, onSelect: () => open(e) })
    if (!multi && canWrite && isEditable(e)) list.push({ label: "Edit", icon: <FilePenIcon />, onSelect: () => navigate(editUrl(placeId, e.path)) })
    list.push({ label: multi ? `Download ${sel.length} items` : "Download", icon: <DownloadIcon />, onSelect: () => downloadEntries(sel) })
    if (!multi && place?.canShare) list.push({ label: "Share…", icon: <Link2Icon />, onSelect: () => setDialog({ type: "share", target: shareTarget(e) }) })
    if (!multi && !e.isDir && previewKind(e))
      list.push({
        label: e.review?.comments ? `Comments (${e.review.comments})` : "Comments",
        icon: <MessageSquareIcon />,
        onSelect: () => {
          setReviewOpen(true)
          setPreview(files.findIndex((x) => x.path === e.path))
        },
      })
    if (place?.canShare) list.push({ label: "Add to collection…", icon: <LibraryIcon />, onSelect: () => setDialog({ type: "collection", entries: sel }) })
    const allFav = sel.every((x) => x.favorite)
    list.push({
      label: allFav ? "Remove from favourites" : "Add to favourites",
      icon: allFav ? <StarOffIcon /> : <StarIcon />,
      onSelect: () => toggleFavorite(sel, !allFav),
      separator: true,
    })
    if (canWrite) list.push({ label: "Tags…", icon: <TagIcon />, onSelect: () => setDialog({ type: "tags", entries: sel }) })
    if (!multi && !e.isDir && canWrite) {
      if (!e.lock) list.push({ label: "Lock for editing", icon: <LockIcon />, onSelect: () => setLock(e, true) })
      else if (e.lock.source === "coffer" && (e.lock.userId === me.id || me.role === "admin"))
        list.push({ label: e.lock.userId === me.id ? "Unlock" : `Unlock (${e.lock.username}'s lock)`, icon: <UnlockIcon />, onSelect: () => setLock(e, false) })
    }
    if (searching && !multi) list.push({ label: "Show in folder", icon: <FolderInputIcon />, onSelect: () => go(parentPath(e.path)) })
    if (canWrite) {
      if (!multi) list.push({ label: "Rename", icon: <PencilIcon />, onSelect: () => setDialog({ type: "rename", entry: e }), separator: true })
      list.push({ label: "Move to…", icon: <FolderInputIcon />, onSelect: () => setDialog({ type: "move", entries: sel }), separator: multi })
    }
    list.push({ label: "Copy to…", icon: <CopyIcon />, onSelect: () => setDialog({ type: "copy", entries: sel }) })
    if (canBackup) list.push({ label: "Back up…", icon: <DatabaseBackupIcon />, onSelect: () => setDialog({ type: "backup", entries: sel }) })
    if (canDelete) list.push({ label: "Delete", icon: <Trash2Icon />, destructive: true, separator: true, onSelect: () => setDialog({ type: "delete", entries: sel }) })
    return list
  }

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      const t = ev.target as HTMLElement
      if (dialog || preview !== null || t.closest("input, textarea, [role=dialog], [role=menu]")) return
      const mod = ev.metaKey || ev.ctrlKey
      const sel = selection.items
      if (mod && ev.key === "a") {
        ev.preventDefault()
        selection.all()
      } else if (ev.key === "Escape") selection.clear()
      else if ((ev.key === "Delete" || (mod && ev.key === "Backspace")) && sel.length && canDelete) setDialog({ type: "delete", entries: sel })
      else if (ev.key === "F2" && sel.length === 1 && canWrite) setDialog({ type: "rename", entry: sel[0] })
      else if (ev.key === "Enter" && sel.length === 1) open(sel[0])
      else if (ev.key === "Backspace" && !mod && path !== "/") go(parentPath(path))
      else if (ev.key === "/" && !mod) {
        ev.preventDefault()
        document.getElementById("folder-search")?.focus()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  })

  const dropProps = {
    onDragEnter: (ev: DragEvent) => {
      if (!ev.dataTransfer.types.includes("Files") || !canWrite) return
      dragDepth.current++
      setDragging(true)
    },
    onDragOver: (ev: DragEvent) => {
      if (ev.dataTransfer.types.includes("Files") && canWrite) ev.preventDefault()
    },
    onDragLeave: () => {
      dragDepth.current = Math.max(0, dragDepth.current - 1)
      if (!dragDepth.current) setDragging(false)
    },
    onDrop: async (ev: DragEvent) => {
      dragDepth.current = 0
      setDragging(false)
      if (!ev.dataTransfer.types.includes("Files") || !canWrite) return
      ev.preventDefault()
      startUpload(await filesFromDrop(ev.dataTransfer), path)
    },
  }

  const error = listing.error as ApiError | null
  const sel = selection.items
  const title = path === "/" ? (place?.name ?? "Files") : baseName(path)
  const parts = path.split("/").filter(Boolean)
  // Always set, even at a drive's root, so the header keeps its shape.
  const parents = parts.length
    ? [{ label: place?.name ?? "Files", path: "/" }, ...parts.slice(0, -1).map((p, i) => ({ label: p, path: "/" + parts.slice(0, i + 1).join("/") }))].map(
        (c) => ({ label: c.label, to: filesUrl(placeId, c.path) }),
      )
    : []
  const sharedBy = place?.kind === "shared" && place.grantedBy ? ` · shared by ${place.grantedBy}` : ""

  const newMenu = canWrite && (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button aria-label="New">
          <PlusIcon /> <span className="hidden sm:inline">New</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuItem onSelect={() => setDialog({ type: "newFolder" })}>
          <FolderPlusIcon /> New folder
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => setDialog({ type: "newFile", kind: "doc" })}>
          <FilePlusIcon /> New document
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => setDialog({ type: "newFile", kind: "sheet" })}>
          <SheetIcon /> New spreadsheet (CSV)
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => fileInput.current?.click()}>
          <FileUpIcon /> Upload files
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => folderInput.current?.click()}>
          <FolderUpIcon /> Upload folder
        </DropdownMenuItem>
        {place?.canShare && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => setDialog({ type: "send" })}>
              <SendIcon /> Send files…
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )

  return (
    <div className="relative min-h-full" {...dropProps}>
      <Page
        title={title}
        parents={parents}
        description={
          // Empty folders still say "0 B · 0 files", and a loading one keeps the
          // line's height, so the header never jumps between folders.
          listing.data ? (
            <span className="inline-flex flex-wrap items-center gap-x-2">
              {`${formatBytes(listing.data.size)} · ${fileCount(listing.data.items)}${sharedBy}`}
              <BackupBadge mark={listing.data.backup} label />
            </span>
          ) : (
            "\u00a0"
          )
        }
        actions={newMenu}
      >
        {prefs.quickCreate && canWrite && path === "/" && !searching && (
          <div className="mb-6 flex gap-3 overflow-x-auto pb-1">
            <QuickCard icon={FilePlusIcon} label="New document" onClick={() => setDialog({ type: "newFile", kind: "doc" })} />
            {place?.canShare && <QuickCard icon={SendIcon} label="Send files" onClick={() => setDialog({ type: "send" })} />}
            <QuickCard icon={FolderPlusIcon} label="New folder" onClick={() => setDialog({ type: "newFolder" })} />
            <QuickCard icon={UploadCloudIcon} label="Upload files" onClick={() => fileInput.current?.click()} />
          </div>
        )}

        <section>
          <div className="mb-3 flex flex-col gap-2 xl:flex-row xl:items-center">
            <div className="-mx-1 flex min-w-0 flex-1 overflow-x-auto px-1">
              <div className="inline-flex gap-1.5">
                {kindFilters.map((k) => (
                  <button
                    key={k.id}
                    type="button"
                    onClick={() => setKindTab(k.id)}
                    className={cn(
                      "h-7 shrink-0 rounded-full border bg-card px-3 text-[0.8125rem] font-medium whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground",
                      kindTab === k.id && "border-foreground bg-foreground text-background hover:text-background",
                    )}
                  >
                    {k.label}
                  </button>
                ))}
              </div>
            </div>
            <div className="flex items-center gap-2">
              <div className="relative flex-1 xl:w-52 xl:flex-none 2xl:w-72">
                <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  id="folder-search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  // The phone keyboard: a Search key, and no capitals or corrections in file names.
                  enterKeyHint="search"
                  autoCapitalize="none"
                  autoCorrect="off"
                  spellCheck={false}
                  placeholder={path === "/" ? `Search ${place?.name ?? "files"}` : `Search in ${baseName(path)}`}
                  className="pl-8"
                />
                {query && (
                  <button type="button" className="absolute top-1/2 right-2.5 -translate-y-1/2 text-muted-foreground" onClick={() => setQuery("")} aria-label="Clear search">
                    {results.isFetching ? <Spinner /> : <XIcon className="size-4" />}
                  </button>
                )}
              </div>
              <Button variant="outline" size="icon" onClick={() => setDialog({ type: "activity" })} aria-label="Activity" title="Activity">
                <HistoryIcon />
              </Button>
              <ToggleGroup type="single" variant="outline" value={prefs.view} onValueChange={(v) => v && setPrefs({ view: v as "list" | "grid" })}>
                <ToggleGroupItem value="list" aria-label="List view" className="w-8">
                  <ListIcon />
                </ToggleGroupItem>
                <ToggleGroupItem value="grid" aria-label="Grid view" className="w-8">
                  <LayoutGridIcon />
                </ToggleGroupItem>
              </ToggleGroup>
            </div>
          </div>

          {sel.length > 0 && (
            // Phones: a floating toolbar above the tab bar, like Files on iOS.
            <div className="sticky top-[calc(env(safe-area-inset-top)+0.5rem)] z-20 mb-3 flex flex-wrap items-center gap-1 rounded-xl border bg-popover/95 px-2 py-1.5 shadow-sm backdrop-blur max-md:fixed max-md:inset-x-3 max-md:top-auto max-md:bottom-[calc(var(--tabbar-h)+env(safe-area-inset-bottom)+0.5rem)] max-md:z-40 max-md:mb-0 max-md:flex-nowrap max-md:justify-between max-md:overflow-x-auto max-md:gap-0 max-md:rounded-2xl max-md:py-1 max-md:shadow-lg max-md:[&_[data-slot=button]]:h-10 max-md:[&_[data-slot=button]]:min-w-9">
              <Button variant="ghost" size="icon-sm" onClick={selection.clear} aria-label="Clear selection">
                <XIcon />
              </Button>
              <span className="mr-2 text-sm font-medium whitespace-nowrap">{sel.length} selected</span>
              <Button variant="ghost" size="sm" onClick={() => downloadEntries(sel)}>
                <DownloadIcon /> <span className="hidden md:inline">Download</span>
              </Button>
              {sel.length === 1 && place?.canShare && (
                <Button variant="ghost" size="sm" onClick={() => setDialog({ type: "share", target: shareTarget(sel[0]) })}>
                  <Link2Icon /> <span className="hidden md:inline">Share</span>
                </Button>
              )}
              {canWrite && (
                <>
                  {sel.length === 1 && (
                    <Button variant="ghost" size="sm" onClick={() => setDialog({ type: "rename", entry: sel[0] })}>
                      <PencilIcon /> <span className="hidden md:inline">Rename</span>
                    </Button>
                  )}
                  <Button variant="ghost" size="sm" onClick={() => setDialog({ type: "move", entries: sel })}>
                    <FolderInputIcon /> <span className="hidden md:inline">Move</span>
                  </Button>
                </>
              )}
              <Button variant="ghost" size="sm" onClick={() => setDialog({ type: "copy", entries: sel })}>
                <CopyIcon /> <span className="hidden md:inline">Copy</span>
              </Button>
              {canBackup && (
                <Button variant="ghost" size="sm" onClick={() => setDialog({ type: "backup", entries: sel })}>
                  <DatabaseBackupIcon /> <span className="hidden md:inline">Back up</span>
                </Button>
              )}
              {canDelete && (
                <Button variant="ghost" size="sm" className="text-destructive" onClick={() => setDialog({ type: "delete", entries: sel })}>
                  <Trash2Icon /> <span className="hidden md:inline">Delete</span>
                </Button>
              )}
            </div>
          )}

          {searching && (
            <p className="mb-3 text-sm text-muted-foreground">
              {results.data ? `${results.data.entries.length}${results.data.entries.length >= 300 ? "+" : ""} results for “${term}” in ${results.data.ms} ms` : `Searching for “${term}”…`}
            </p>
          )}

          {listing.isLoading ? (
            <div className="space-y-2">
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} className="h-11 w-full rounded-lg" />
              ))}
            </div>
          ) : error ? (
            <EmptyState
              icon={<XIcon />}
              title={error.status === 404 ? "Folder not found" : error.code === "drive_offline" ? "This drive is offline" : "Could not load this folder"}
              description={error.status === 404 ? "It may have been moved, deleted, or you no longer have access." : error.message}
            >
              <Button variant="outline" onClick={() => navigate("/files")}>
                Go to my files
              </Button>
            </EmptyState>
          ) : entries.length === 0 ? (
            searching || kinds.length ? (
              <EmptyState icon={<SearchIcon />} title="Nothing matches" description="Try another name or file type." />
            ) : (
              <div className="rounded-xl border border-dashed bg-card/50 py-6">
                <EmptyState
                  icon={<UploadCloudIcon />}
                  title="This folder is empty"
                  description={canWrite ? "Drag files or folders here, or upload from your computer." : "Nothing has been added yet."}
                >
                  {canWrite && (
                    <div className="flex gap-2">
                      <Button onClick={() => fileInput.current?.click()}>
                        <FileUpIcon /> Upload files
                      </Button>
                      <Button variant="outline" onClick={() => setDialog({ type: "newFolder" })}>
                        <FolderPlusIcon /> New folder
                      </Button>
                    </div>
                  )}
                </EmptyState>
              </div>
            )
          ) : (
            <FileList
              entries={entries}
              view={prefs.view}
              selection={selection}
              sort={sort}
              onSort={setSort}
              onOpen={open}
              thumbUrl={(e) => api.thumbUrl(placeId, e)}
              actions={actionsFor}
              onMove={canWrite && !searching ? move : undefined}
              onDropFiles={canWrite ? async (dt, dest) => startUpload(await filesFromDrop(dt), dest) : undefined}
              location={searching ? (e) => parentPath(e.path) : undefined}
              sharedPaths={sharedPaths}
              tagColor={tagColor}
              onTag={(t) => navigate(`/tags/${encodeURIComponent(t)}`)}
              onToggleStack={(e) =>
                setStacksOpen((s) => {
                  const n = new Set(s)
                  const k = stackKey(e)
                  if (n.has(k)) n.delete(k)
                  else n.add(k)
                  return n
                })
              }
              stackOpen={(e) => stacksOpen.has(stackKey(e))}
              pageSize={prefs.pageSize}
              resetKey={`${placeId}${path}${term}${kindTab}`}
            />
          )}
        </section>
      </Page>

      <input
        ref={fileInput}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          if (e.target.files) startUpload(filesFromInput(e.target.files), path)
          e.target.value = ""
        }}
      />
      <input
        ref={folderInput}
        type="file"
        hidden
        {...({ webkitdirectory: "" } as object)}
        onChange={(e) => {
          if (e.target.files) startUpload(filesFromInput(e.target.files), path)
          e.target.value = ""
        }}
      />

      {dragging && (
        <div className="pointer-events-none absolute inset-3 z-30 flex items-center justify-center rounded-2xl border-2 border-dashed border-brand bg-brand/10 backdrop-blur-[1px]">
          <div className="flex flex-col items-center gap-2 rounded-xl bg-popover px-6 py-4 shadow-lg">
            <UploadCloudIcon className="size-8 text-brand" />
            <p className="text-sm font-medium">Drop to upload to {title}</p>
          </div>
        </div>
      )}

      <PreviewDialog
        entries={files}
        index={preview}
        onIndexChange={(i) => {
          setPreview(i)
          if (i === null) setReviewOpen(false)
        }}
        downloadUrl={(p, inline) => api.downloadUrl(placeId, p, inline)}
        onEdit={canWrite ? (e) => navigate(editUrl(placeId, e.path)) : undefined}
        mediaUrl={(e, kind) => api.mediaUrl(placeId, e, kind)}
        review={{
          adapter: (e) => ({
            list: () => api.comments(placeId, e.path),
            add: (input) => api.addComment(placeId, { ...input, path: e.path }).finally(() => qc.invalidateQueries({ queryKey: ["list", placeId] })),
            resolve: api.resolveComment,
            remove: api.deleteComment,
          }),
          queryKey: (e) => ["comments", placeId, e.path],
          allowInternal: true,
        }}
        showReview={reviewOpen}
      />

      <NameDialog
        open={dialog?.type === "newFolder"}
        title="New folder"
        label="Folder name"
        initial="Untitled folder"
        submitLabel="Create"
        onClose={() => setDialog(null)}
        onSubmit={async (name) => {
          await api.mkdir(placeId, path, name)
          refresh()
        }}
      />
      <NameDialog
        open={dialog?.type === "newFile"}
        title={dialog?.type === "newFile" && dialog.kind === "sheet" ? "New spreadsheet" : "New document"}
        label="File name"
        initial={dialog?.type === "newFile" && dialog.kind === "sheet" ? "Untitled.csv" : "Untitled.md"}
        submitLabel="Create and edit"
        onClose={() => setDialog(null)}
        onSubmit={async (name) => {
          const sheet = name.toLowerCase().endsWith(".csv")
          const p = path === "/" ? `/${name}` : `${path}/${name}`
          await api.writeText({ place: placeId, path: p, content: sheet ? "Name,Value\n" : `# ${name.replace(/\.[^.]+$/, "")}\n\n`, create: true })
          refresh()
          navigate(editUrl(placeId, p))
        }}
      />
      <NameDialog
        open={dialog?.type === "rename"}
        title="Rename"
        label="New name"
        initial={dialog?.type === "rename" ? dialog.entry.name : ""}
        submitLabel="Rename"
        onClose={() => setDialog(null)}
        onSubmit={async (name) => {
          if (dialog?.type !== "rename" || name === dialog.entry.name) return
          await api.rename(placeId, dialog.entry.path, name)
          selection.clear()
          refresh()
          qc.invalidateQueries({ queryKey: ["shares"] })
        }}
      />
      <DeleteDialog
        open={dialog?.type === "delete"}
        count={dialog?.type === "delete" ? dialog.entries.length : 0}
        name={dialog?.type === "delete" ? dialog.entries[0]?.name : undefined}
        onClose={() => setDialog(null)}
        onConfirm={async (permanent) => {
          if (dialog?.type !== "delete") return
          const paths = dialog.entries.map((e) => e.path)
          setDialog(null)
          try {
            report(await api.remove(placeId, paths, permanent), permanent ? "deleted" : "moved to trash")
            qc.invalidateQueries({ queryKey: ["trash"] })
          } catch (e) {
            toast.error((e as Error).message)
          }
        }}
      />
      <FolderPickerDialog
        open={dialog?.type === "move" || dialog?.type === "copy"}
        mode={dialog?.type === "copy" ? "copy" : "move"}
        count={dialog?.type === "move" || dialog?.type === "copy" ? dialog.entries.length : 0}
        startPlace={placeId}
        start={path}
        exclude={dialog?.type === "move" || dialog?.type === "copy" ? dialog.entries.filter((e) => e.isDir).map((e) => e.path) : []}
        onClose={() => setDialog(null)}
        onPick={async (destPlace, dest) => {
          if (dialog?.type !== "move" && dialog?.type !== "copy") return
          const { type, entries: list } = dialog
          setDialog(null)
          const paths = list.map((e) => e.path)
          const t = toast.loading(`${type === "move" ? "Moving" : "Copying"} ${paths.length} item${paths.length === 1 ? "" : "s"}…`)
          try {
            const r = type === "move" ? await api.move(placeId, paths, destPlace, dest) : await api.copy(placeId, paths, destPlace, dest)
            toast.dismiss(t)
            report(r, type === "move" ? "moved" : "copied")
          } catch (e) {
            toast.dismiss(t)
            toast.error((e as Error).message)
          }
        }}
      />
      <ShareDialog
        open={dialog?.type === "share"}
        target={dialog?.type === "share" ? dialog.target : undefined}
        canUpload={canWrite}
        onClose={() => setDialog(null)}
      />
      <TagsDialog
        open={dialog?.type === "tags"}
        place={placeId}
        entries={dialog?.type === "tags" ? dialog.entries : []}
        onClose={() => setDialog(null)}
        onDone={refresh}
      />
      <CollectionPickerDialog
        open={dialog?.type === "collection"}
        place={placeId}
        paths={dialog?.type === "collection" ? dialog.entries.map((e) => e.path) : []}
        onClose={() => setDialog(null)}
      />
      <BackupDialog
        open={dialog?.type === "backup"}
        place={place}
        entries={dialog?.type === "backup" ? dialog.entries : []}
        onClose={() => setDialog(null)}
        onDone={() => {
          selection.clear()
          refresh()
        }}
      />
      <SendDialog open={dialog?.type === "send"} onClose={() => setDialog(null)} />
      <ActivitySheet open={dialog?.type === "activity"} place={placeId} path={path} title={title} onClose={() => setDialog(null)} />
      <ConflictDialog
        open={dialog?.type === "conflict"}
        names={dialog?.type === "conflict" ? dialog.names : []}
        folders={dialog?.type === "conflict" ? dialog.folders.length : 0}
        canReplace={canDelete}
        onClose={() => setDialog(null)}
        onChoose={resolveConflict}
      />
    </div>
  )
}

/** The first part of an upload's relative name: the file, or the dropped folder. */
function topName(name: string) {
  return name.split("/")[0]
}

export type { Hit }
