import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { DatabaseBackupIcon, DownloadIcon, RotateCcwIcon, XIcon } from "lucide-react"
import { useNavigate, useParams, useSearchParams } from "react-router"
import { toast } from "sonner"

import { FileTile } from "@/components/file-icon"
import { Page, type PageParent } from "@/components/page"
import { PreviewDialog } from "@/components/preview-dialog"
import { EmptyState, Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { api, type Entry, type SnapshotEntry } from "@/lib/api"
import { fileKind, filesUrl, formatBytes, formatCount, formatDate, previewKind } from "@/lib/format"

const snapLabel = (created: number) => new Date(created * 1000).toLocaleString([], { dateStyle: "medium", timeStyle: "short" })

function download(url: string) {
  const a = document.createElement("a")
  a.href = url
  a.download = ""
  document.body.appendChild(a)
  a.click()
  a.remove()
}

const asEntry = (e: SnapshotEntry): Entry => ({ ...e, kind: fileKind(e) })

/**
 * A backup snapshot as a read-only folder view: preview and download files
 * straight from the backup drive, download folders or a selection as a zip,
 * or restore them next to the originals.
 */
export default function SnapshotPage() {
  const { id = "" } = useParams()
  const navigate = useNavigate()
  const [search, setSearch] = useSearchParams()
  const backups = useQuery({ queryKey: ["backups"], queryFn: api.backups })
  const backup = backups.data?.find((b) => b.id === id)
  const snaps = useQuery({ queryKey: ["snapshots", id], queryFn: () => api.snapshots(id), enabled: !!backup })
  const snap = search.get("snap") || snaps.data?.[0]?.name || ""
  const path = search.get("path") || "/"
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [preview, setPreview] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)

  const listing = useQuery({
    queryKey: ["snapshot", id, snap, path],
    queryFn: () => api.browseSnapshot(id, snap, path),
    enabled: !!backup && !!snap,
  })
  const entries = useMemo(() => listing.data?.entries ?? [], [listing.data])
  const previewable = useMemo(() => entries.filter((e) => !e.isDir).map(asEntry), [entries])

  const go = (next: { snap?: string; path?: string }) => {
    const sp = new URLSearchParams(search)
    if (next.snap !== undefined) sp.set("snap", next.snap)
    if (next.path !== undefined) {
      if (next.path === "/") sp.delete("path")
      else sp.set("path", next.path)
    }
    setSearch(sp)
    setPicked(new Set())
  }

  const toggle = (p: string) => {
    const next = new Set(picked)
    if (next.has(p)) next.delete(p)
    else next.add(p)
    setPicked(next)
  }
  const allPicked = entries.length > 0 && picked.size === entries.length
  const chosen = entries.filter((e) => picked.has(e.path))
  const chosenSize = chosen.reduce((n, e) => n + e.size, 0)
  const folderSize = entries.reduce((n, e) => n + e.size, 0)

  const downloadEntries = (list: SnapshotEntry[]) => {
    if (list.length === 1 && !list[0].isDir) return download(api.snapshotFileUrl(id, snap, list[0].path))
    download(api.snapshotZipUrl(id, snap, path, list.length ? list.map((e) => e.name) : undefined))
  }

  const open = (e: SnapshotEntry) => {
    if (e.isDir) return go({ path: e.path })
    if (previewKind(e)) setPreview(previewable.findIndex((x) => x.path === e.path))
    else download(api.snapshotFileUrl(id, snap, e.path))
  }

  const restore = async (list: SnapshotEntry[]) => {
    if (!backup) return
    setBusy(true)
    try {
      const paths = list.length ? list.map((e) => e.path.replace(/^\//, "")) : [path.replace(/^\//, "") || "."]
      const res = await api.restoreBackup(backup.id, snap, paths)
      toast.success(`Restoring into “${res.into.split("/").pop()}”`, {
        description: `${backup.sourceName}:${res.into}. You'll get a notification when it's done.`,
        action: { label: "Open", onClick: () => navigate(filesUrl(`d-${res.drive}`, res.into)) },
      })
      setPicked(new Set())
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const crumbs = path === "/" ? [] : path.split("/").filter(Boolean)
  const parents: PageParent[] = [
    { label: "Backups", to: "/admin/backups" },
    ...(crumbs.length ? [{ label: backup?.name ?? "Backup", onClick: () => go({ path: "/" }) }] : []),
    ...crumbs.slice(0, -1).map((c, i) => ({ label: c, onClick: () => go({ path: "/" + crumbs.slice(0, i + 1).join("/") }) })),
  ]
  const snapInfo = snaps.data?.find((s) => s.name === snap)

  if (backups.isSuccess && !backup) {
    return (
      <Page title="Backup not found" parents={[{ label: "Backups", to: "/admin/backups" }]}>
        <EmptyState icon={<DatabaseBackupIcon />} title="This backup no longer exists" description="It may have been deleted." />
      </Page>
    )
  }

  return (
    <Page
      title={crumbs.at(-1) ?? backup?.name ?? "Backup"}
      parents={parents}
      description={
        backup && (
          <>
            Read-only copy of {backup.sourceName}:/{backup.sourcePath === "." ? "" : backup.sourcePath}
            {snapInfo && ` as it was on ${snapLabel(snapInfo.created)}`}. Downloads come straight from {backup.targetName}.
          </>
        )
      }
      actions={
        snaps.data && snaps.data.length > 0 ? (
          <Select value={snap} onValueChange={(v) => go({ snap: v })}>
            <SelectTrigger className="w-56" aria-label="Snapshot">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {snaps.data.map((s, i) => (
                <SelectItem key={s.name} value={s.name}>
                  {snapLabel(s.created)}
                  {i === 0 && " (latest)"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : undefined
      }
    >
      {backups.isLoading || snaps.isLoading ? (
        <Skeleton className="h-64 w-full rounded-xl" />
      ) : !snaps.data?.length ? (
        <EmptyState icon={<DatabaseBackupIcon />} title="No snapshots yet" description="Run the backup first. Each run adds a snapshot you can browse here." />
      ) : (
        <>
          <div className="mb-3 flex min-h-9 flex-wrap items-center gap-2">
            {picked.size > 0 ? (
              <>
                <span className="text-sm font-medium">
                  {picked.size} selected · {formatBytes(chosenSize)}
                </span>
                <Button variant="ghost" size="sm" onClick={() => setPicked(new Set())}>
                  <XIcon /> Clear
                </Button>
                <div className="flex-1" />
                <Button variant="outline" size="sm" onClick={() => restore(chosen)} disabled={busy}>
                  {busy ? <Spinner /> : <RotateCcwIcon />} Restore
                </Button>
                <Button size="sm" onClick={() => downloadEntries(chosen)}>
                  <DownloadIcon /> Download
                </Button>
              </>
            ) : (
              <>
                <span className="text-sm text-muted-foreground">
                  {formatCount(entries.length)} item{entries.length === 1 ? "" : "s"} · {formatBytes(folderSize)}
                  {path === "/" && listing.data && ` · ${formatCount(listing.data.files)} files in this snapshot`}
                </span>
                <div className="flex-1" />
                <Button variant="outline" size="sm" onClick={() => restore([])} disabled={busy || !entries.length}>
                  {busy ? <Spinner /> : <RotateCcwIcon />} Restore {path === "/" ? "everything" : "folder"}
                </Button>
                <Button size="sm" onClick={() => downloadEntries([])} disabled={!entries.length}>
                  <DownloadIcon /> Download {path === "/" ? "everything" : "folder"}
                </Button>
              </>
            )}
          </div>

          <div className="overflow-hidden rounded-xl border bg-card">
            <div className="flex items-center gap-3 border-b bg-muted/40 px-3 py-2 text-xs font-medium text-muted-foreground">
              <Checkbox
                checked={allPicked ? true : picked.size ? "indeterminate" : false}
                onCheckedChange={() => setPicked(allPicked ? new Set() : new Set(entries.map((e) => e.path)))}
                aria-label="Select all"
                disabled={!entries.length}
              />
              <span className="flex-1">Name</span>
              <span className="hidden w-24 text-right sm:block">Size</span>
              <span className="hidden w-40 md:block">Modified</span>
              <span className="w-8" />
            </div>
            {listing.isLoading ? (
              <div className="p-6">
                <Spinner />
              </div>
            ) : listing.isError ? (
              <p className="p-6 text-sm text-destructive">{(listing.error as Error).message}</p>
            ) : !entries.length ? (
              <p className="p-6 text-sm text-muted-foreground">This folder is empty in this snapshot.</p>
            ) : (
              entries.map((e) => (
                <div key={e.path} className="group flex items-center gap-3 border-b px-3 py-[var(--row-y)] last:border-b-0 hover:bg-muted/50" data-picked={picked.has(e.path)}>
                  <Checkbox checked={picked.has(e.path)} onCheckedChange={() => toggle(e.path)} aria-label={`Select ${e.name}`} />
                  <button type="button" className="flex min-w-0 flex-1 items-center gap-3 text-left" onClick={() => open(e)}>
                    <FileTile entry={e} size="sm" />
                    <span className="min-w-0">
                      <span className="block truncate text-sm font-medium group-hover:underline">{e.name}</span>
                      {e.isDir && e.items ? <span className="block text-xs text-muted-foreground">{formatCount(e.items)} files</span> : null}
                    </span>
                  </button>
                  <span className="hidden w-24 shrink-0 text-right text-sm text-muted-foreground tabular-nums sm:block">{formatBytes(e.size)}</span>
                  <span className="hidden w-40 shrink-0 text-sm text-muted-foreground md:block">{e.modTime ? formatDate(e.modTime) : "—"}</span>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="shrink-0 opacity-60 group-hover:opacity-100"
                    onClick={() => downloadEntries([e])}
                    aria-label={e.isDir ? `Download ${e.name} as a zip` : `Download ${e.name}`}
                    title={e.isDir ? "Download as a zip" : "Download"}
                  >
                    <DownloadIcon />
                  </Button>
                </div>
              ))
            )}
          </div>
        </>
      )}

      <PreviewDialog
        entries={previewable}
        index={preview}
        onIndexChange={setPreview}
        downloadUrl={(p, inline) => api.snapshotFileUrl(id, snap, p, inline)}
      />
    </Page>
  )
}
