import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { CloudIcon, HardDriveIcon, LockIcon } from "lucide-react"
import { toast } from "sonner"

import { Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api, type Backup, type Drive, type Entry, type Place } from "@/lib/api"
import { cn } from "@/lib/utils"

export function DriveOption({ d }: { d: Drive }) {
  return (
    <>
      {d.kind === "s3" ? <CloudIcon /> : <HardDriveIcon />}
      {d.name}
      {d.s3?.encrypt && <LockIcon aria-label="encrypted" />}
      {!d.online && " (offline)"}
    </>
  )
}

const within = (parent: string, p: string) => parent === "." || p === parent || p.startsWith(parent + "/")
const shown = (p: string) => (p === "." ? "/" : "/" + p)

/** Adds picked files and folders to a backup, or starts a new one. */
export function BackupDialog({ open, place, entries, onClose, onDone }: {
  open: boolean
  place?: Place
  entries: Entry[]
  onClose: () => void
  onDone: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-lg">
        {place && <Body place={place} entries={entries} onClose={onClose} onDone={onDone} />}
      </DialogContent>
    </Dialog>
  )
}

function Body({ place, entries, onClose, onDone }: { place: Place; entries: Entry[]; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient()
  const backups = useQuery({ queryKey: ["backups"], queryFn: api.backups })
  const drives = useQuery({ queryKey: ["drives"], queryFn: api.drives })
  // Choices stay "automatic" (null) until picked, so they follow the data as it loads.
  const [choice, setChoice] = useState<string | null>(null)
  const [name, setName] = useState(entries.length === 1 ? entries[0].name : "")
  const [target, setTarget] = useState<string | null>(null)
  const [runNow, setRunNow] = useState(true)
  const [busy, setBusy] = useState(false)

  // Drive paths, as backups store them.
  const root = place.root.replace(/^\/+|\/+$/g, "")
  const paths = entries.map((e) => [root, e.path.replace(/^\/+|\/+$/g, "")].filter(Boolean).join("/") || ".")
  const what = entries.length === 1 ? `“${entries[0].name}”` : `${entries.length} items`

  const options = (backups.data ?? [])
    .filter((b) => b.sourceDrive === place.driveId)
    .map((b) => {
      const roots = b.items.length ? b.items : [b.sourcePath]
      return {
        b,
        outside: paths.some((p) => !within(b.sourcePath, p)),
        covered: paths.every((p) => roots.some((r) => within(r, p))),
        // Taken out only as they were added: one by one.
        removable: b.items.length > paths.length && paths.every((p) => b.items.includes(p)),
      }
    })
  const usable = options.filter((o) => !o.outside && !o.covered)

  const list = drives.data?.drives ?? []
  // Buckets first, encrypted ones before the rest; this drive last.
  const rank = (d: Drive) => (d.kind === "s3" ? 2 : 0) + (d.s3?.encrypt ? 1 : 0) - (d.id === place.driveId ? 4 : 0)
  const targets = [...list].sort((a, b) => rank(b) - rank(a))
  const targetId = target ?? targets.find((d) => d.online && d.id !== place.driveId)?.id ?? ""
  const targetDrive = list.find((d) => d.id === targetId)

  const picked = choice ?? (usable[0]?.b.id || "new")
  const making = picked === "new"

  const submit = async (remove?: Backup) => {
    setBusy(true)
    try {
      const r = await api.backupItems({
        place: place.id,
        paths: entries.map((e) => e.path),
        ...(remove ? { backup: remove.id, remove: true } : making ? { name: name.trim(), targetDrive: targetId } : { backup: picked }),
        run: !remove && runNow,
      })
      const where = `“${r.backup.name}”`
      if (remove) toast.success(`Taken out of ${where}`, { description: "Copies already made stay until they age out of its snapshots." })
      else if (making) toast.success(`Backing up ${what} to ${r.backup.targetName}`, { description: r.started ? "Started now." : `Every night at ${String(r.backup.hour).padStart(2, "0")}:00.` })
      else if (r.added) toast.success(`Added ${what} to ${where}`, { description: r.started ? "Backing up now." : "Backed up tonight." })
      else toast.info(`Already in ${where}`, { description: r.started ? "Backing up now." : undefined })
      qc.invalidateQueries({ queryKey: ["backups"] })
      onDone()
      onClose()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid gap-5">
      <DialogHeader>
        <DialogTitle>Back up {what}</DialogTitle>
        <DialogDescription>
          Copies are made every night, with their full folder path kept, so a restore puts them back where they were.
        </DialogDescription>
      </DialogHeader>

      {backups.isLoading || drives.isLoading ? (
        <div className="flex justify-center py-6">
          <Spinner />
        </div>
      ) : (
        <RadioGroup value={picked} onValueChange={setChoice} className="gap-2">
          {options.map(({ b, outside, covered, removable }) => {
            const disabled = outside || covered
            return (
              <div
                key={b.id}
                className={cn("flex items-start gap-3 rounded-lg border p-3", picked === b.id && "border-ring", disabled && "bg-muted/40")}
              >
                <RadioGroupItem value={b.id} id={`bk-${b.id}`} disabled={disabled} className="mt-0.5" />
                <Label htmlFor={`bk-${b.id}`} className={cn("grid flex-1 gap-0.5 font-normal", disabled && "cursor-default")}>
                  <span className="font-medium">Add to “{b.name}”</span>
                  <span className="flex items-center gap-1 text-xs text-muted-foreground">
                    {b.targetKind === "s3" ? <CloudIcon className="size-3" /> : <HardDriveIcon className="size-3" />}
                    {b.targetName}
                    {list.find((d) => d.id === b.targetDrive)?.s3?.encrypt && <LockIcon className="size-3" aria-label="encrypted" />}
                    {!b.enabled && " · paused"}
                  </span>
                  {covered && <span className="text-xs text-emerald-700 dark:text-emerald-400">Already backed up here</span>}
                  {outside && <span className="text-xs text-muted-foreground">Only backs up {shown(b.sourcePath)}</span>}
                </Label>
                {removable && (
                  <Button type="button" variant="ghost" size="sm" className="text-destructive" disabled={busy} onClick={() => submit(b)}>
                    Stop backing up
                  </Button>
                )}
              </div>
            )
          })}
          <div className={cn("grid gap-3 rounded-lg border p-3", making && "border-ring")}>
            <div className="flex items-center gap-3">
              <RadioGroupItem value="new" id="bk-new" />
              <Label htmlFor="bk-new" className="font-medium">
                New backup
              </Label>
            </div>
            {making && (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="bk-name">Name</FieldLabel>
                  <Input id="bk-name" maxLength={80} placeholder="Business critical" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
                </Field>
                <Field>
                  <FieldLabel>To</FieldLabel>
                  <Select value={targetId} onValueChange={setTarget}>
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
                {targetDrive?.kind === "s3" && !targetDrive.s3?.encrypt && (
                  <p className="flex gap-2 text-sm text-amber-700 sm:col-span-2 dark:text-amber-400">
                    <LockIcon className="mt-0.5 size-4 shrink-0" />
                    {targetDrive.name} isn't encrypted, so the bucket's provider can read these copies.
                  </p>
                )}
                {targetDrive?.id === place.driveId && (
                  <p className="text-sm text-amber-700 sm:col-span-2 dark:text-amber-400">
                    A copy on the same drive won't help if the drive fails. Pick another drive or a bucket if you can.
                  </p>
                )}
              </div>
            )}
          </div>
        </RadioGroup>
      )}

      <div className="flex items-center gap-2">
        <Checkbox id="bk-run" checked={runNow} onCheckedChange={(v) => setRunNow(v === true)} />
        <Label htmlFor="bk-run" className="font-normal">
          Start backing up now
        </Label>
      </div>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          Cancel
        </Button>
        <Button type="button" disabled={busy || backups.isLoading || (making && (!name.trim() || !targetId))} onClick={() => submit()}>
          {busy && <Spinner />} {making ? "Create backup" : "Add to backup"}
        </Button>
      </DialogFooter>
    </div>
  )
}
