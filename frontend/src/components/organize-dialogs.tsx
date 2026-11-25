import { useEffect, useMemo, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ArrowRightLeftIcon,
  CheckIcon,
  DownloadIcon,
  FilePenIcon,
  FolderPlusIcon,
  HistoryIcon,
  LibraryIcon,
  LinkIcon,
  LockIcon,
  MessageSquareIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
  UnlockIcon,
  UploadIcon,
} from "lucide-react"
import { toast } from "sonner"

import { Spinner } from "@/components/states"
import { swatch, TagChip, TAG_COLORS } from "@/components/tag-chip"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { api, type Activity, type Entry } from "@/lib/api"
import { formatDate, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

export function useTagColors() {
  const tags = useQuery({ queryKey: ["tags"], queryFn: api.tags, staleTime: 60_000 })
  return useMemo(() => {
    const m = new Map<string, string>()
    for (const t of tags.data ?? []) if (t.color) m.set(t.tag.toLowerCase(), t.color)
    return (tag: string) => m.get(tag.toLowerCase())
  }, [tags.data])
}

/** Add or remove tags on one or more items. */
export function TagsDialog({ open, place, entries, onClose, onDone }: { open: boolean; place: string; entries: Entry[]; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient()
  const all = useQuery({ queryKey: ["tags"], queryFn: api.tags, enabled: open })
  const color = useTagColors()
  const current = useMemo(() => {
    const counts = new Map<string, number>()
    for (const e of entries) for (const t of e.tags ?? []) counts.set(t, (counts.get(t) ?? 0) + 1)
    return counts
  }, [entries])
  const [on, setOn] = useState<Set<string>>(new Set())
  const [draft, setDraft] = useState("")
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (open) {
      setOn(new Set([...current].filter(([, n]) => n === entries.length).map(([t]) => t)))
      setDraft("")
    }
  }, [open, current, entries.length])

  const known = [...new Set([...(all.data ?? []).map((t) => t.tag), ...current.keys()])].sort((a, b) => a.localeCompare(b))
  const toggle = (t: string) => setOn((s) => {
    const n = new Set(s)
    if (n.has(t)) n.delete(t)
    else n.add(t)
    return n
  })
  const addDraft = () => {
    const t = draft.trim().replace(/^#/, "")
    if (!t) return
    setOn((s) => new Set(s).add(t))
    setDraft("")
  }
  const save = async () => {
    setBusy(true)
    try {
      const add = [...on]
      const remove = [...current.keys()].filter((t) => !on.has(t))
      await api.editTags(place, entries.map((e) => e.path), add, remove)
      qc.invalidateQueries({ queryKey: ["tags"] })
      onDone()
      onClose()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const setColor = async (t: string, c: string) => {
    await api.setTagColor(t, c).catch((e) => toast.error(e.message))
    qc.invalidateQueries({ queryKey: ["tags"] })
  }
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Tags</DialogTitle>
          <DialogDescription>
            {entries.length === 1 ? `For “${entries[0]?.name}”.` : `For ${entries.length} items.`} Everyone who can see an item sees its tags.
          </DialogDescription>
        </DialogHeader>
        <div className="flex gap-1.5">
          <Input
            placeholder="New tag, e.g. Acme or Q4 launch"
            value={draft}
            maxLength={40}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault()
                addDraft()
              }
            }}
          />
          <Button variant="outline" onClick={addDraft} disabled={!draft.trim()}>
            <PlusIcon /> Add
          </Button>
        </div>
        <div className="max-h-64 space-y-0.5 overflow-y-auto">
          {[...new Set([...known, ...on])].map((t) => (
            <div key={t} className="group flex items-center gap-2 rounded-md px-1.5 py-1 hover:bg-muted">
              <Checkbox id={`tag-${t}`} checked={on.has(t)} onCheckedChange={() => toggle(t)} />
              <label htmlFor={`tag-${t}`} className="flex-1 cursor-pointer">
                <TagChip tag={t} color={color(t)} />
              </label>
              <div className="flex gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
                {TAG_COLORS.map((c) => (
                  <button key={c} type="button" onClick={() => setColor(t, c)} aria-label={`${c} colour for ${t}`} className={cn("size-3.5 rounded-full ring-offset-1 ring-offset-background", swatch[c], color(t) === c && "ring-2 ring-ring")} />
                ))}
              </div>
            </div>
          ))}
          {!known.length && !on.size && <p className="py-4 text-center text-sm text-muted-foreground">No tags yet. Add one above.</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy}>
            {busy && <Spinner />} Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** Add items to an existing or new collection. */
export function CollectionPickerDialog({ open, place, paths, onClose }: { open: boolean; place: string; paths: string[]; onClose: () => void }) {
  const qc = useQueryClient()
  const cols = useQuery({ queryKey: ["collections"], queryFn: api.collections, enabled: open })
  const [name, setName] = useState("")
  const [busy, setBusy] = useState<string | null>(null)
  useEffect(() => {
    if (open) setName("")
  }, [open])
  const done = (n: string) => {
    toast.success(`Added to “${n}”`, { action: { label: "Open", onClick: () => (window.location.href = "/collections") } })
    qc.invalidateQueries({ queryKey: ["collections"] })
    onClose()
  }
  const add = async (id: string, n: string) => {
    setBusy(id)
    try {
      await api.addToCollection(id, place, paths)
      done(n)
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(null)
    }
  }
  const create = async () => {
    setBusy("new")
    try {
      const c = await api.createCollection({ name: name.trim(), place, paths })
      done(c.name)
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(null)
    }
  }
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Add to collection</DialogTitle>
          <DialogDescription>Collections gather files from anywhere into one list you can share with a single link. Nothing is moved or copied.</DialogDescription>
        </DialogHeader>
        <div className="max-h-64 space-y-1 overflow-y-auto">
          {cols.isLoading ? (
            <Spinner />
          ) : (
            (cols.data ?? []).map((c) => (
              <button
                key={c.id}
                type="button"
                onClick={() => add(c.id, c.name)}
                disabled={!!busy}
                className="flex w-full items-center gap-3 rounded-lg border px-3 py-2 text-left hover:bg-muted disabled:opacity-60"
              >
                <LibraryIcon className="size-4 text-muted-foreground" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{c.name}</span>
                  <span className="block text-xs text-muted-foreground">
                    {c.items.length} {c.items.length === 1 ? "item" : "items"}
                    {c.links ? ` · ${c.links} ${c.links === 1 ? "link" : "links"}` : ""}
                  </span>
                </span>
                {busy === c.id ? <Spinner /> : <PlusIcon className="size-4 text-muted-foreground" />}
              </button>
            ))
          )}
        </div>
        <div className="flex gap-1.5 border-t pt-3">
          <Input placeholder="New collection name" value={name} maxLength={120} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && name.trim() && create()} />
          <Button onClick={create} disabled={!name.trim() || !!busy}>
            {busy === "new" ? <Spinner /> : <FolderPlusIcon />} Create
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

const actionText: Record<string, [string, typeof PencilIcon]> = {
  upload: ["uploaded", UploadIcon],
  "share.upload": ["uploaded via a link", UploadIcon],
  rename: ["renamed", PencilIcon],
  move: ["moved", ArrowRightLeftIcon],
  copy: ["copied", ArrowRightLeftIcon],
  delete: ["deleted", Trash2Icon],
  "delete.permanent": ["permanently deleted", Trash2Icon],
  restore: ["restored", HistoryIcon],
  "folder.create": ["created folder", FolderPlusIcon],
  "file.create": ["created", FilePenIcon],
  "file.edit": ["edited", FilePenIcon],
  "share.create": ["created a link to", LinkIcon],
  "share.update": ["changed the link to", LinkIcon],
  "share.revoke": ["deleted the link to", LinkIcon],
  "share.invite": ["sent", LinkIcon],
  "share.download": ["downloaded", DownloadIcon],
  "share.download_zip": ["downloaded", DownloadIcon],
  "share.verified": ["confirmed their email for", CheckIcon],
  "share.invite_opened": ["opened their invitation to", CheckIcon],
  "review.comment": ["commented on", MessageSquareIcon],
  "review.approved": ["approved", CheckIcon],
  "review.changes": ["asked for changes to", MessageSquareIcon],
  lock: ["locked", LockIcon],
  unlock: ["unlocked", UnlockIcon],
  "grant.create": ["shared", LinkIcon],
}

function describe(a: Activity) {
  const [verb, Icon] = actionText[a.action] ?? [a.action.replace(/[._]/g, " "), HistoryIcon]
  return { verb, Icon }
}

/** Who did what in a folder and below it, newest first. */
export function ActivitySheet({ open, place, path, title, onClose }: { open: boolean; place: string; path: string; title: string; onClose: () => void }) {
  const [extra, setExtra] = useState<Activity[]>([])
  const [more, setMore] = useState(true)
  const first = useQuery({ queryKey: ["activity", place, path], queryFn: () => api.activity(place, path), enabled: open })
  useEffect(() => {
    setExtra([])
    setMore(true)
  }, [place, path, open])
  const list = [...(first.data ?? []), ...extra]
  const loadMore = async () => {
    const last = list[list.length - 1]
    if (!last) return
    const next = await api.activity(place, path, last.id).catch(() => [])
    if (next.length < 50) setMore(false)
    setExtra((x) => [...x, ...next])
  }
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b">
          <SheetTitle>Activity</SheetTitle>
          <SheetDescription>Everything that happened in “{title}” and the folders inside it.</SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-y-auto px-4 py-2">
          {first.isLoading ? (
            <Spinner />
          ) : list.length === 0 ? (
            <p className="py-10 text-center text-sm text-muted-foreground">Nothing yet.</p>
          ) : (
            <ol className="relative space-y-3 border-l pl-4">
              {list.map((a) => {
                const { verb, Icon } = describe(a)
                return (
                  <li key={a.id} className="relative">
                    <span className="absolute top-0.5 -left-[1.4rem] flex size-4.5 items-center justify-center rounded-full border bg-background">
                      <Icon className="size-2.5 text-muted-foreground" />
                    </span>
                    <p className="text-sm leading-snug">
                      <span className="font-medium">{a.who || "Someone"}</span>
                      {a.guest && <span className="text-muted-foreground"> (guest)</span>} {verb}{" "}
                      <span className="font-medium break-all">{a.name || a.path}</span>
                      {a.detail && /^to \//.test(a.detail) && <span className="text-muted-foreground"> {a.detail}</span>}
                    </p>
                    <p className="text-xs text-muted-foreground" title={formatDate(a.ts)}>
                      {relativeTime(a.ts)}
                      {a.path !== "/" && <> · {a.path}</>}
                    </p>
                  </li>
                )
              })}
            </ol>
          )}
          {list.length >= 50 && more && (
            <Button variant="ghost" size="sm" className="mt-3 w-full" onClick={loadMore}>
              Show older
            </Button>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
