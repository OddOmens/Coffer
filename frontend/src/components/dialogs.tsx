import { useEffect, useRef, useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { ChevronRightIcon, FolderIcon, HomeIcon } from "lucide-react"

import { PathBreadcrumbs } from "@/components/path-breadcrumbs"
import { Spinner } from "@/components/states"
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
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { usePlaces } from "@/components/app-layout"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api"

export function NameDialog({
  open,
  title,
  label,
  initial,
  submitLabel,
  onClose,
  onSubmit,
}: {
  open: boolean
  title: string
  label: string
  initial: string
  submitLabel: string
  onClose: () => void
  onSubmit: (name: string) => Promise<unknown>
}) {
  const [value, setValue] = useState(initial)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!open) return
    setValue(initial)
    setError("")
    // Select the name without its extension, like Finder.
    requestAnimationFrame(() => {
      const dot = initial.lastIndexOf(".")
      input.current?.setSelectionRange(0, dot > 0 ? dot : initial.length)
    })
  }, [open, initial])

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    const name = value.trim()
    if (!name) return
    setBusy(true)
    try {
      await onSubmit(name)
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          <Field data-invalid={!!error}>
            <FieldLabel htmlFor="name-input">{label}</FieldLabel>
            <Input
              id="name-input"
              ref={input}
              value={value}
              autoFocus
              aria-invalid={!!error}
              onChange={(e) => {
                setValue(e.target.value)
                setError("")
              }}
            />
            {error && <FieldError>{error}</FieldError>}
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !value.trim()}>
              {busy && <Spinner />}
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function DeleteDialog({
  open,
  count,
  name,
  onClose,
  onConfirm,
}: {
  open: boolean
  count: number
  name?: string
  onClose: () => void
  onConfirm: (permanent: boolean) => void
}) {
  const [permanent, setPermanent] = useState(false)
  useEffect(() => setPermanent(false), [open])
  const what = count === 1 && name ? `“${name}”` : `${count} items`
  return (
    <AlertDialog open={open} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {what}?</AlertDialogTitle>
          <AlertDialogDescription>
            {permanent
              ? "This cannot be undone. Share links to these items stop working."
              : "Items go to the trash, where they can be restored until it is emptied."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex items-center gap-2">
          <Checkbox id="perm" checked={permanent} onCheckedChange={(v) => setPermanent(v === true)} />
          <Label htmlFor="perm" className="font-normal">
            Delete permanently (skip the trash)
          </Label>
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={() => onConfirm(permanent)}>
            {permanent ? "Delete forever" : "Move to trash"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** Picks a destination folder (on any place) for move / copy. */
export function FolderPickerDialog({
  open,
  mode,
  count,
  startPlace,
  start,
  exclude,
  onClose,
  onPick,
}: {
  open: boolean
  mode: "move" | "copy"
  count: number
  startPlace: string
  start: string
  exclude: string[] // folder paths in startPlace that cannot be targets
  onClose: () => void
  onPick: (place: string, dest: string) => void
}) {
  const [place, setPlace] = useState(startPlace)
  const [path, setPath] = useState(start)
  const places = usePlaces()
  useEffect(() => {
    if (open) {
      setPlace(startPlace)
      setPath(start)
    }
  }, [open, startPlace, start])
  const { data, isLoading } = useQuery({ queryKey: ["list", place, path], queryFn: () => api.list(place, path), enabled: open && !!place })
  const sameP = place === startPlace
  const folders = (data?.entries ?? []).filter((e) => e.isDir && !(sameP && exclude.includes(e.path)))
  const blocked = (sameP && exclude.some((p) => path === p || path.startsWith(p + "/"))) || data?.place?.canWrite === false
  const writable = (places.data?.places ?? []).filter((p) => p.canWrite && p.online)
  const placeName = writable.find((p) => p.id === place)?.name ?? "Files"

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {mode === "move" ? "Move" : "Copy"} {count} item{count === 1 ? "" : "s"}
          </DialogTitle>
          <DialogDescription>Choose a destination folder. Other drives work too.</DialogDescription>
        </DialogHeader>
        {writable.length > 1 && (
          <Select
            value={place}
            onValueChange={(v) => {
              setPlace(v)
              setPath("/")
            }}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {writable.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name}
                  {p.kind === "shared" && p.grantedBy ? ` · from ${p.grantedBy}` : ""}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <div className="rounded-lg border">
          <div className="border-b px-3 py-2">
            <PathBreadcrumbs path={path} rootLabel={placeName} rootIcon={<HomeIcon className="size-3.5" />} onNavigate={setPath} />
          </div>
          <div className="h-72 overflow-y-auto p-1">
            {isLoading ? (
              <div className="flex h-full items-center justify-center">
                <Spinner />
              </div>
            ) : folders.length === 0 ? (
              <p className="p-6 text-center text-sm text-muted-foreground">No sub-folders here.</p>
            ) : (
              folders.map((f) => (
                <button
                  key={f.path}
                  type="button"
                  onClick={() => setPath(f.path)}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-muted"
                >
                  <FolderIcon className="size-4 fill-sky-500/20 text-sky-500" />
                  <span className="flex-1 truncate">{f.name}</span>
                  <ChevronRightIcon className="size-4 text-muted-foreground" />
                </button>
              ))
            )}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={blocked} onClick={() => onPick(place, path)}>
            {mode === "move" ? "Move here" : "Copy here"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function ConflictDialog({
  open,
  names,
  folders = 0,
  canReplace,
  onClose,
  onChoose,
}: {
  open: boolean
  names: string[]
  /** How many of names are folders being uploaded into. */
  folders?: number
  canReplace: boolean
  onClose: () => void
  onChoose: (choice: "replace" | "keep" | "skip") => void
}) {
  return (
    <AlertDialog open={open} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {names.length === 1 ? `“${names[0]}” already exists` : `${names.length} items already exist`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {folders > 0
              ? "Replace files with the same name (the old versions go to the trash), keep both by uploading into a numbered folder, or skip what's already there and upload only what's new."
              : "Replace them (the old versions go to the trash), keep both with a numbered name, or skip them."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {names.length > 1 && (
          <ul className="max-h-32 overflow-y-auto rounded-md border p-2 text-sm text-muted-foreground">
            {names.map((n) => (
              <li key={n} className="truncate">
                {n}
              </li>
            ))}
          </ul>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => onChoose("skip")}>Skip</AlertDialogCancel>
          <Button variant="outline" onClick={() => onChoose("keep")}>
            Keep both
          </Button>
          {canReplace && <AlertDialogAction onClick={() => onChoose("replace")}>Replace</AlertDialogAction>}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
