import { useEffect, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ChevronRightIcon,
  CloudIcon,
  DatabaseBackupIcon,
  FileIcon,
  FolderIcon,
  FolderOpenIcon,
  HardDriveIcon,
  HistoryIcon,
  LockIcon,
  MoreHorizontalIcon,
  PlayIcon,
  PlusIcon,
  RotateCcwIcon,
  Trash2Icon,
  TriangleAlertIcon,
  XIcon,
} from "lucide-react"
import { Link, useNavigate } from "react-router"
import { toast } from "sonner"

import { DriveOption } from "@/components/backup-dialog"
import { AdminPage } from "@/components/admin-page"
import { EmptyState, Spinner } from "@/components/states"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api, type Backup, type BackupRun, type Drive } from "@/lib/api"
import { filesUrl, formatBytes, formatCount, formatDate, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

const hourLabel = (h: number) => `${String(h).padStart(2, "0")}:00`

/** "in 5 h" / "in 40 min" until a unix time. */
function untilText(unix: number) {
  const s = unix - Date.now() / 1000
  if (s <= 0) return "soon"
  if (s < 3600) return `in ${Math.max(1, Math.round(s / 60))} min`
  return `in ${Math.round(s / 3600)} h`
}

function where(name: string, path: string) {
  return `${name}:${path === "." ? "/" : "/" + path}`
}

export default function BackupsPage() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ["backups"],
    queryFn: api.backups,
    refetchInterval: (q) => (q.state.data?.some((b) => b.running) ? 2000 : 30_000),
  })
  const [creating, setCreating] = useState(false)
  const [history, setHistory] = useState<Backup | null>(null)
  const [restoring, setRestoring] = useState<Backup | null>(null)
  const [deleting, setDeleting] = useState<Backup | null>(null)

  const refresh = () => qc.invalidateQueries({ queryKey: ["backups"] })

  const run = async (b: Backup, force = false) => {
    try {
      await api.runBackup(b.id, force)
      toast.success(`Backing up ${b.name}`)
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  const toggle = async (b: Backup, enabled: boolean) => {
    try {
      await api.updateBackup(b.id, { enabled })
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  const dropItem = async (b: Backup, p: string) => {
    try {
      await api.updateBackup(b.id, { items: b.items.filter((x) => x !== p) })
      toast.success(`Taken out of ${b.name}`, { description: "Copies already made stay until they age out of its snapshots." })
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  const remove = async () => {
    if (!deleting) return
    try {
      await api.deleteBackup(deleting.id)
      toast.success(`${deleting.name} deleted`)
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    }
    setDeleting(null)
  }

  return (
    <AdminPage
      description="Nightly copies of a drive folder, or of files and folders you pick in Files (select them, then Back up), to another drive such as a bucket. Only changed files are uploaded, and earlier versions stay restorable."
      actions={
        <Button onClick={() => setCreating(true)}>
          <PlusIcon /> New backup
        </Button>
      }
    >
      {isLoading ? (
        <Skeleton className="h-40 w-full rounded-xl" />
      ) : !data?.length ? (
        <div className="rounded-xl border border-dashed bg-card/50 py-6">
          <EmptyState icon={<DatabaseBackupIcon />} title="No backups yet" description="Back up your main drive to R2 every night, with point-in-time restore.">
            <Button onClick={() => setCreating(true)}>
              <PlusIcon /> New backup
            </Button>
          </EmptyState>
        </div>
      ) : (
        <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
          {data.map((b) => (
            <BackupCard
              key={b.id}
              b={b}
              onRun={() => run(b)}
              onForce={() => run(b, true)}
              onToggle={(v) => toggle(b, v)}
              onHistory={() => setHistory(b)}
              onRestore={() => setRestoring(b)}
              onDelete={() => setDeleting(b)}
              onDropItem={(p) => dropItem(b, p)}
            />
          ))}
        </div>
      )}
      <CreateDialog open={creating} onClose={() => setCreating(false)} onCreated={refresh} />
      <HistoryDialog backup={history} onClose={() => setHistory(null)} />
      <RestoreDialog backup={restoring} onClose={() => setRestoring(null)} />
      <AlertDialog open={!!deleting} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {deleting?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Nightly runs stop. The backed-up files stay in {deleting && where(deleting.targetName, deleting.targetPath)}; delete them there if you no longer need them.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={remove}>
              Delete backup
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </AdminPage>
  )
}

function RunState({ run }: { run: BackupRun }) {
  if (run.state === "failed") return <Badge variant="destructive">Failed</Badge>
  if (run.state === "running") return <Badge variant="secondary">Running</Badge>
  return <Badge className="bg-emerald-600 text-white">Done</Badge>
}

function BackupCard({
  b,
  onRun,
  onForce,
  onToggle,
  onHistory,
  onRestore,
  onDelete,
  onDropItem,
}: {
  b: Backup
  onRun: () => void
  onForce: () => void
  onToggle: (v: boolean) => void
  onHistory: () => void
  onRestore: () => void
  onDelete: () => void
  onDropItem: (path: string) => void
}) {
  const last = b.lastRun
  const Icon = b.targetKind === "s3" ? CloudIcon : HardDriveIcon
  return (
    <div className={cn("rounded-xl border bg-card p-4", !b.enabled && "opacity-80")}>
      <div className="flex items-start gap-3">
        <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-tile text-tile-foreground">
          <DatabaseBackupIcon className="size-5 stroke-[1.75]" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="truncate font-semibold">{b.name}</span>
            {!b.enabled && <Badge variant="outline">Paused</Badge>}
          </div>
          <div className="mt-1 grid grid-cols-[2.5rem_1fr] gap-x-2 gap-y-0.5 text-xs">
            <span className="text-muted-foreground">From</span>
            {b.items.length ? (
              <ul className="grid min-w-0 gap-0.5">
                {b.items.map((p) => (
                  <li key={p} className="group flex min-w-0 items-center gap-1 font-mono">
                    <span className="truncate" title={where(b.sourceName, p)}>
                      {where(b.sourceName, p)}
                    </span>
                    {b.items.length > 1 && (
                      <button
                        type="button"
                        onClick={() => onDropItem(p)}
                        className="shrink-0 rounded text-muted-foreground opacity-60 hover:text-destructive hover:opacity-100 focus-visible:opacity-100"
                        aria-label={`Stop backing up ${p}`}
                        title="Stop backing up"
                      >
                        <XIcon className="size-3" />
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <span className="truncate font-mono" title={where(b.sourceName, b.sourcePath)}>
                {where(b.sourceName, b.sourcePath)}
              </span>
            )}
            <span className="text-muted-foreground">To</span>
            <span className="flex min-w-0 items-center gap-1 font-mono" title={where(b.targetName, b.targetPath)}>
              <Icon className="size-3 shrink-0 text-muted-foreground" />
              <span className="truncate">{where(b.targetName, b.targetPath)}</span>
            </span>
          </div>
        </div>
        <Switch checked={b.enabled} onCheckedChange={onToggle} aria-label={b.enabled ? "Pause nightly runs" : "Resume nightly runs"} />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${b.name}`}>
              <MoreHorizontalIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={onRun} disabled={!!b.running}>
              <PlayIcon /> Run now
            </DropdownMenuItem>
            <DropdownMenuItem asChild>
              <Link to={`/admin/backups/${b.id}/browse`}>
                <FolderOpenIcon /> Browse snapshots
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onRestore}>
              <RotateCcwIcon /> Restore…
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onHistory}>
              <HistoryIcon /> History
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onDelete}>
              <Trash2Icon /> Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <div className="mt-3 text-xs text-muted-foreground">
        Nightly at {hourLabel(b.hour)} server time · keeps {b.keep} snapshot{b.keep === 1 ? "" : "s"}, plus one a week for 8 weeks and one a month for a year
        {b.enabled && b.nextRun > 0 && !b.running && ` · next ${untilText(b.nextRun)}`}
      </div>

      <div className="mt-3 border-t pt-3 text-sm">
        {b.running ? (
          <div>
            <div className="flex items-center gap-2 font-medium">
              <Spinner /> Backing up…
            </div>
            <div className="mt-1 text-xs text-muted-foreground tabular-nums">
              {formatCount(b.running.files)} files checked ({formatBytes(b.running.bytes)}) · {formatCount(b.running.uploaded)} uploaded (
              {formatBytes(b.running.uploadedBytes)})
            </div>
            <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-secondary">
              <div className="h-full w-1/3 animate-[backup-progress_1.4s_ease-in-out_infinite] rounded-full bg-brand" />
            </div>
            <style>{`@keyframes backup-progress{0%{transform:translateX(-100%)}100%{transform:translateX(300%)}}`}</style>
          </div>
        ) : last ? (
          <div className="grid gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <RunState run={last} />
              <span className="text-muted-foreground" title={formatDate(last.startedAt)}>
                {relativeTime(last.finishedAt || last.startedAt)}
              </span>
            </div>
            {last.state === "failed" ? (
              <>
                <p className="text-xs break-words text-destructive">{last.error}</p>
                {b.canForce && (
                  <div>
                    <Button variant="outline" size="sm" className="mt-1" onClick={onForce}>
                      <PlayIcon /> Run anyway
                    </Button>
                  </div>
                )}
              </>
            ) : (
              <>
                <p className="text-xs text-muted-foreground tabular-nums">
                  {formatCount(last.files)} files ({formatBytes(last.bytes)}) · {formatCount(last.uploaded)} new or changed ({formatBytes(last.uploadedBytes)})
                </p>
                {last.error && (
                  <p className="flex gap-1.5 text-xs break-words text-amber-700 dark:text-amber-400">
                    <TriangleAlertIcon className="mt-px size-3.5 shrink-0" />
                    {last.error}
                  </p>
                )}
              </>
            )}
          </div>
        ) : (
          <div className="flex items-center justify-between gap-2">
            <span className="text-muted-foreground">Not run yet.</span>
            <Button variant="outline" size="sm" onClick={onRun}>
              <PlayIcon /> Run now
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

function CreateDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: () => void }) {
  const drives = useQuery({ queryKey: ["drives"], queryFn: api.drives, enabled: open })
  const list = drives.data?.drives ?? []
  // Buckets first, encrypted ones before the rest.
  const rank = (d: Drive) => (d.kind === "s3" ? 1 : 0) + (d.s3?.encrypt ? 1 : 0)
  const targets = [...list].sort((a, b) => rank(b) - rank(a))
  const blank = { name: "", sourceDrive: "", sourcePath: ".", targetDrive: "", targetPath: "", hour: 2, keep: 30 }
  const [form, setForm] = useState(blank)
  const [pathEdited, setPathEdited] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    setForm(blank)
    setPathEdited(false)
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!open || !list.length) return
    setForm((f) => ({
      ...f,
      // The main drive first, then any other local drive.
      sourceDrive:
        f.sourceDrive || list.find((d) => d.online && d.id === "files")?.id || list.find((d) => d.online && d.kind === "local")?.id || list[0].id,
      targetDrive: f.targetDrive || targets.find((d) => d.online && d.kind === "s3")?.id || "",
    }))
  }, [open, drives.data]) // eslint-disable-line react-hooks/exhaustive-deps

  const defaultTarget = `.backups/${(form.name.trim() || "backup").replace(/\//g, "-")}`
  const targetPath = pathEdited ? form.targetPath : defaultTarget

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    setBusy(true)
    try {
      await api.createBackup({ ...form, name: form.name.trim(), targetPath: targetPath.replace(/^\/+/, "") })
      toast.success("Backup created")
      onCreated()
      onClose()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-lg">
        <form onSubmit={submit} className="grid gap-5">
          <DialogHeader>
            <DialogTitle>New backup</DialogTitle>
            <DialogDescription>Runs every night. Each run uploads only new and changed files and records a snapshot you can restore from.</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <Field>
              <FieldLabel htmlFor="b-name">Name</FieldLabel>
              <Input id="b-name" required maxLength={80} placeholder="Studio files" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            </Field>
            <div className="grid gap-3 sm:grid-cols-[12rem_1fr]">
              <Field>
                <FieldLabel>Back up</FieldLabel>
                <Select value={form.sourceDrive} onValueChange={(v) => setForm({ ...form, sourceDrive: v })}>
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="Drive" />
                  </SelectTrigger>
                  <SelectContent>
                    {list.map((d) => (
                      <SelectItem key={d.id} value={d.id} disabled={!d.online}>
                        <DriveOption d={d} />
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="b-src">Folder</FieldLabel>
                <Input id="b-src" className="font-mono" value={form.sourcePath} onChange={(e) => setForm({ ...form, sourcePath: e.target.value })} />
              </Field>
            </div>
            <div className="grid gap-3 sm:grid-cols-[12rem_1fr]">
              <Field>
                <FieldLabel>To</FieldLabel>
                <Select value={form.targetDrive} onValueChange={(v) => setForm({ ...form, targetDrive: v })}>
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="Drive" />
                  </SelectTrigger>
                  <SelectContent>
                    {targets.map((d) => (
                      <SelectItem key={d.id} value={d.id} disabled={!d.online}>
                        <DriveOption d={d} />
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="b-dst">Folder</FieldLabel>
                <Input
                  id="b-dst"
                  className="font-mono"
                  value={targetPath}
                  onChange={(e) => {
                    setPathEdited(true)
                    setForm({ ...form, targetPath: e.target.value })
                  }}
                />
              </Field>
            </div>
            {(() => {
              const t = list.find((d) => d.id === form.targetDrive)
              return t?.kind === "s3" && !t.s3?.encrypt ? (
                <p className="-mt-2 flex gap-2 text-sm text-amber-700 dark:text-amber-400">
                  <LockIcon className="mt-0.5 size-4 shrink-0" />
                  {t.name} isn't encrypted, so the bucket's provider can read these backups. For a private copy, add a drive with Encrypt files on
                  under Drives and back up to that.
                </p>
              ) : null
            })()}
            <FieldDescription className="-mt-2">
              {targets.some((d) => d.kind === "s3")
                ? "A bucket such as Cloudflare R2 keeps a copy off site. Folders starting with a dot stay out of search and browsing."
                : "Add an R2 or S3 drive in Drives to keep a copy off site. Folders starting with a dot stay out of search and browsing."}
            </FieldDescription>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field>
                <FieldLabel>Start at (server time)</FieldLabel>
                <Select value={String(form.hour)} onValueChange={(v) => setForm({ ...form, hour: Number(v) })}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {Array.from({ length: 24 }, (_, h) => (
                      <SelectItem key={h} value={String(h)}>
                        {hourLabel(h)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="b-keep">Snapshots to keep</FieldLabel>
                <Input
                  id="b-keep"
                  type="number"
                  min={1}
                  max={3650}
                  value={form.keep}
                  onChange={(e) => setForm({ ...form, keep: Math.max(1, Number(e.target.value) || 1) })}
                />
              </Field>
            </div>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !form.name.trim() || !form.sourceDrive || !form.targetDrive}>
              {busy && <Spinner />} Create backup
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function HistoryDialog({ backup, onClose }: { backup: Backup | null; onClose: () => void }) {
  const runs = useQuery({ queryKey: ["backup-runs", backup?.id], queryFn: () => api.backupRuns(backup!.id), enabled: !!backup })
  return (
    <Dialog open={!!backup} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>History of {backup?.name}</DialogTitle>
          <DialogDescription>The last 30 runs.</DialogDescription>
        </DialogHeader>
        {runs.isLoading ? (
          <Skeleton className="h-32 w-full rounded-lg" />
        ) : !runs.data?.length ? (
          <p className="text-sm text-muted-foreground">No runs yet.</p>
        ) : (
          <div className="overflow-hidden rounded-lg border">
            <Table>
              <TableHeader className="bg-muted/40">
                <TableRow>
                  <TableHead className="pl-3">Started</TableHead>
                  <TableHead>Result</TableHead>
                  <TableHead className="hidden text-right sm:table-cell">Files</TableHead>
                  <TableHead className="text-right">Uploaded</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {runs.data.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell className="pl-3 whitespace-nowrap">{formatDate(r.startedAt)}</TableCell>
                    <TableCell>
                      <div className="flex flex-col gap-0.5">
                        <RunState run={r} />
                        {r.error && (
                          <span className={cn("max-w-64 truncate text-xs", r.state === "failed" ? "text-destructive" : "text-amber-700 dark:text-amber-400")} title={r.error}>
                            {r.error}
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="hidden text-right tabular-nums sm:table-cell">{formatCount(r.files)}</TableCell>
                    <TableCell className="text-right tabular-nums">
                      {formatCount(r.uploaded)} · {formatBytes(r.uploadedBytes)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

const snapLabel = (created: number) => new Date(created * 1000).toLocaleString([], { dateStyle: "medium", timeStyle: "short" })

function RestoreDialog({ backup, onClose }: { backup: Backup | null; onClose: () => void }) {
  const navigate = useNavigate()
  const snaps = useQuery({ queryKey: ["snapshots", backup?.id], queryFn: () => api.snapshots(backup!.id), enabled: !!backup })
  const [snap, setSnap] = useState("")
  const [path, setPath] = useState("/")
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setSnap("")
    setPath("/")
    setPicked(new Set())
  }, [backup])

  useEffect(() => {
    if (!snap && snaps.data?.length) setSnap(snaps.data[0].name)
  }, [snaps.data, snap])

  const listing = useQuery({
    queryKey: ["snapshot", backup?.id, snap, path],
    queryFn: () => api.browseSnapshot(backup!.id, snap, path),
    enabled: !!backup && !!snap,
  })

  const toggle = (p: string) => {
    const next = new Set(picked)
    if (next.has(p)) next.delete(p)
    else next.add(p)
    setPicked(next)
  }

  const crumbs = path === "/" ? [] : path.split("/").filter(Boolean)

  const restore = async () => {
    if (!backup || !snap) return
    setBusy(true)
    try {
      const paths = [...picked].map((p) => p.replace(/^\//, ""))
      const res = await api.restoreBackup(backup.id, snap, paths)
      toast.success(`Restoring into “${res.into.split("/").pop()}”`, {
        description: `${backup.sourceName}:${res.into}. You'll get a notification when it's done.`,
        action: { label: "Open", onClick: () => navigate(filesUrl(`d-${res.drive}`, res.into)) },
      })
      onClose()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!backup} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Restore from {backup?.name}</DialogTitle>
          <DialogDescription>Files are copied into a new “Restored …” folder next to the originals. Nothing is overwritten.</DialogDescription>
        </DialogHeader>
        {snaps.isLoading ? (
          <Skeleton className="h-40 w-full rounded-lg" />
        ) : !snaps.data?.length ? (
          <p className="text-sm text-muted-foreground">No snapshots yet. Run the backup first.</p>
        ) : (
          <div className="grid gap-3">
            <Field>
              <FieldLabel>Snapshot</FieldLabel>
              <Select
                value={snap}
                onValueChange={(v) => {
                  setSnap(v)
                  setPath("/")
                  setPicked(new Set())
                }}
              >
                <SelectTrigger className="w-full sm:w-72">
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
            </Field>

            <nav className="flex min-w-0 flex-wrap items-center gap-1 text-sm">
              <button type="button" className="rounded px-1 text-muted-foreground hover:text-foreground" onClick={() => setPath("/")}>
                {backup?.sourceName}
                {backup && backup.sourcePath !== "." && `/${backup.sourcePath}`}
              </button>
              {crumbs.map((c, i) => (
                <span key={i} className="flex items-center gap-1">
                  <ChevronRightIcon className="size-3.5 text-muted-foreground" />
                  <button
                    type="button"
                    className={cn("rounded px-1", i === crumbs.length - 1 ? "font-medium" : "text-muted-foreground hover:text-foreground")}
                    onClick={() => setPath("/" + crumbs.slice(0, i + 1).join("/"))}
                  >
                    {c}
                  </button>
                </span>
              ))}
            </nav>

            <div className="max-h-80 overflow-y-auto rounded-lg border">
              {listing.isLoading ? (
                <div className="p-4">
                  <Spinner />
                </div>
              ) : !listing.data?.entries.length ? (
                <p className="p-4 text-sm text-muted-foreground">This folder is empty in that snapshot.</p>
              ) : (
                listing.data.entries.map((e) => (
                  <div key={e.path} className="flex items-center gap-3 border-b px-3 py-2 last:border-b-0 hover:bg-muted/50">
                    <Checkbox checked={picked.has(e.path)} onCheckedChange={() => toggle(e.path)} aria-label={`Select ${e.name}`} />
                    {e.isDir ? <FolderIcon className="size-4 shrink-0 text-sky-500" /> : <FileIcon className="size-4 shrink-0 text-muted-foreground" />}
                    {e.isDir ? (
                      <button type="button" className="min-w-0 flex-1 truncate text-left text-sm font-medium hover:underline" onClick={() => setPath(e.path)}>
                        {e.name}
                      </button>
                    ) : (
                      <span className="min-w-0 flex-1 truncate text-sm">{e.name}</span>
                    )}
                    <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                      {formatBytes(e.size)}
                      {e.isDir && e.items ? ` · ${formatCount(e.items)} files` : ""}
                    </span>
                  </div>
                ))
              )}
            </div>
            <p className="text-xs text-muted-foreground">
              {picked.size ? `${picked.size} selected.` : `Nothing selected: everything in the snapshot (${formatCount(listing.data?.files ?? 0)} files) is restored.`}
            </p>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          {picked.size > 0 && (
            <Button variant="ghost" onClick={() => setPicked(new Set())}>
              Clear selection
            </Button>
          )}
          <Button onClick={restore} disabled={busy || !snap}>
            {busy ? <Spinner /> : <RotateCcwIcon />} {picked.size ? `Restore ${picked.size} item${picked.size === 1 ? "" : "s"}` : "Restore everything"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
