import { useMemo, useState, type FormEvent, type KeyboardEvent } from "react"
import { useMutation, useQuery, useQueryClient, type QueryKey } from "@tanstack/react-query"
import {
  CheckCircle2Icon,
  CheckIcon,
  CircleDotIcon,
  ClockIcon,
  EyeOffIcon,
  MapPinIcon,
  MessageSquareIcon,
  ReplyIcon,
  RotateCcwIcon,
  Trash2Icon,
  TriangleAlertIcon,
  XIcon,
} from "lucide-react"
import { toast } from "sonner"

import type { Marker } from "@/components/media-preview"
import { Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import type { Comment, CommentInput } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

/** How the panel talks to the server; the app and share links differ. */
export type ReviewAdapter = {
  list: () => Promise<Comment[]>
  add: (input: Omit<CommentInput, "path">) => Promise<Comment>
  resolve?: (id: number, resolved: boolean) => Promise<unknown>
  remove?: (id: number) => Promise<unknown>
}

export type ReviewPanelProps = {
  adapter: ReviewAdapter
  /** React Query key for this file's comments, e.g. ["comments", place, path]. */
  queryKey: QueryKey
  /** Current video time, captured when someone starts writing. */
  getTime?: () => number | null
  /** Called when writing starts, e.g. to pause the video. */
  onCompose?: () => void
  /** Jump the video to a comment's timecode. */
  onSeek?: (t: number) => void
  /** A spot picked on the image for the next comment (0–1). */
  pin?: { x: number; y: number } | null
  onClearPin?: () => void
  /** Shows "click the image to pin" hints. */
  pinnable?: boolean
  /** Comment highlighted from a marker click. */
  activeId?: number | null
  onActiveChange?: (id: number | null) => void
  /** Team side: allow notes hidden from link visitors. */
  allowInternal?: boolean
  /** Link visitors without a verified email give a name. */
  needsName?: boolean
  /** Show Approve / Request changes (default true). */
  canDecide?: boolean
  className?: string
}

const NAME_KEY = "coffer.reviewer.name"

function savedName() {
  try {
    return localStorage.getItem(NAME_KEY) ?? ""
  } catch {
    return ""
  }
}

export function formatTimecode(sec: number) {
  const t = Math.max(0, Math.floor(sec))
  const h = Math.floor(t / 3600)
  const m = Math.floor((t % 3600) / 60)
  const s = t % 60
  const pad = (n: number) => String(n).padStart(2, "0")
  return h ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
}

/** Comments of one file, shared by the panel and whatever draws markers. */
export function useComments(adapter: ReviewAdapter, queryKey: QueryKey, enabled = true) {
  return useQuery({ queryKey, queryFn: adapter.list, staleTime: 10_000, refetchInterval: 30_000, enabled })
}

/** Top-level comments in reading order: by timecode, then by time written. */
export function threads(comments: Comment[]) {
  const top = comments.filter((c) => c.parentId === 0 && c.kind === "comment")
  top.sort((a, b) => (a.timecode ?? Infinity) - (b.timecode ?? Infinity) || a.id - b.id)
  return top
}

/** Numbered markers for comments pinned to a spot on an image. */
export function markersFrom(comments: Comment[] | undefined, activeId?: number | null): Marker[] {
  if (!comments) return []
  return threads(comments).flatMap((c, i) =>
    c.x != null && c.y != null && !c.resolved ? [{ id: c.id, x: c.x, y: c.y, label: String(i + 1), active: c.id === activeId }] : [],
  )
}

export function latestDecision(comments: Comment[] | undefined) {
  return comments?.filter((c) => c.kind !== "comment").at(-1)
}

export function ReviewPanel({
  adapter,
  queryKey,
  getTime,
  onCompose,
  onSeek,
  pin,
  onClearPin,
  pinnable,
  activeId,
  onActiveChange,
  allowInternal,
  needsName,
  canDecide = true,
  className,
}: ReviewPanelProps) {
  const qc = useQueryClient()
  const comments = useComments(adapter, queryKey)
  const [body, setBody] = useState("")
  const [time, setTime] = useState<number | null>(null)
  const [internal, setInternal] = useState(false)
  const [name, setName] = useState(savedName)
  const [replyTo, setReplyTo] = useState<number | null>(null)
  const [showResolved, setShowResolved] = useState(false)

  const list = useMemo(() => comments.data ?? [], [comments.data])
  const top = useMemo(() => threads(list), [list])
  const replies = useMemo(() => {
    const m = new Map<number, Comment[]>()
    for (const c of list) if (c.parentId) m.set(c.parentId, [...(m.get(c.parentId) ?? []), c])
    return m
  }, [list])
  const decision = latestDecision(list)
  const resolvedCount = top.filter((c) => c.resolved).length
  const shown = top.filter((c) => showResolved || !c.resolved)

  const send = useMutation({
    mutationFn: (input: Omit<CommentInput, "path">) => {
      if (needsName) {
        try {
          localStorage.setItem(NAME_KEY, name.trim())
        } catch {
          /* private mode */
        }
      }
      return adapter.add({ ...input, name: needsName ? name.trim() : undefined })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey }),
    onError: (e) => toast.error((e as Error).message),
  })
  const refresh = () => qc.invalidateQueries({ queryKey })

  const needName = needsName && !name.trim()
  const compose = () => {
    if (time === null && getTime) {
      const t = getTime()
      if (t !== null && Number.isFinite(t)) setTime(t)
    }
    onCompose?.()
  }
  const submit = async (ev?: FormEvent) => {
    ev?.preventDefault()
    if (!body.trim() || needName) return
    await send.mutateAsync({
      body: body.trim(),
      kind: "comment",
      timecode: time,
      x: pin?.x ?? null,
      y: pin?.y ?? null,
      internal: allowInternal ? internal : undefined,
    })
    setBody("")
    setTime(null)
    onClearPin?.()
  }
  const decide = (kind: "approved" | "changes") => {
    if (needName) {
      toast.error("Add your name first")
      return
    }
    send.mutate({ kind, body: body.trim() || undefined }, { onSuccess: () => setBody("") })
  }
  const onKey = (ev: KeyboardEvent<HTMLTextAreaElement>) => {
    if (ev.key === "Enter" && (ev.metaKey || ev.ctrlKey)) submit()
  }

  return (
    <div className={cn("flex min-h-0 flex-col text-sm", className)}>
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <MessageSquareIcon className="size-4 text-muted-foreground" />
        <span className="font-medium">Comments</span>
        {top.length > 0 && <span className="text-xs text-muted-foreground tabular-nums">{top.length - resolvedCount}</span>}
        <div className="flex-1" />
        {resolvedCount > 0 && (
          <button type="button" onClick={() => setShowResolved((v) => !v)} className="text-xs text-muted-foreground hover:text-foreground">
            {showResolved ? "Hide resolved" : `Show ${resolvedCount} resolved`}
          </button>
        )}
      </div>

      {decision && (
        <div
          className={cn(
            "mx-3 mt-3 flex items-center gap-2 rounded-lg border px-2.5 py-2 text-xs",
            decision.kind === "approved"
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
              : "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
          )}
        >
          {decision.kind === "approved" ? <CheckCircle2Icon className="size-4 shrink-0" /> : <TriangleAlertIcon className="size-4 shrink-0" />}
          <span className="min-w-0">
            <span className="font-medium">{decision.kind === "approved" ? "Approved" : "Changes requested"}</span> by {decision.author} ·{" "}
            {relativeTime(decision.createdAt)}
            {decision.body && <span className="mt-0.5 block text-foreground/80">“{decision.body}”</span>}
          </span>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
        {comments.isLoading ? (
          <div className="flex justify-center py-6">
            <Spinner className="text-muted-foreground" />
          </div>
        ) : shown.length === 0 ? (
          <p className="py-6 text-center text-xs text-muted-foreground">
            {top.length ? "All comments are resolved." : getTime ? "No comments yet. Pause where something needs changing and write a note." : pinnable ? "No comments yet. Click the image to pin a note to a spot." : "No comments yet."}
          </p>
        ) : (
          <ul className="space-y-1">
            {shown.map((c) => (
              <li key={c.id}>
                <CommentItem
                  c={c}
                  number={top.indexOf(c) + 1}
                  active={c.id === activeId}
                  onFocus={() => onActiveChange?.(c.id === activeId ? null : c.id)}
                  onSeek={onSeek}
                  onReply={() => setReplyTo(replyTo === c.id ? null : c.id)}
                  onResolve={adapter.resolve ? () => adapter.resolve!(c.id, !c.resolved).then(refresh, (e) => toast.error(e.message)) : undefined}
                  onRemove={adapter.remove ? () => adapter.remove!(c.id).then(refresh, (e) => toast.error(e.message)) : undefined}
                />
                {(replies.get(c.id) ?? []).map((r) => (
                  <div key={r.id} className="ml-6 border-l pl-2">
                    <CommentItem c={r} onRemove={adapter.remove ? () => adapter.remove!(r.id).then(refresh, (e) => toast.error(e.message)) : undefined} />
                  </div>
                ))}
                {replyTo === c.id && (
                  <ReplyBox
                    busy={send.isPending}
                    disabled={needName}
                    onCancel={() => setReplyTo(null)}
                    onSend={async (text) => {
                      await send.mutateAsync({ body: text, kind: "comment", parentId: c.id, internal: allowInternal ? internal : undefined })
                      setReplyTo(null)
                    }}
                  />
                )}
              </li>
            ))}
          </ul>
        )}
      </div>

      <form onSubmit={submit} className="space-y-2 border-t p-3">
        {needsName && (
          <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder="Your name" aria-label="Your name" className="h-8" />
        )}
        {(time !== null || pin) && (
          <div className="flex flex-wrap gap-1.5">
            {time !== null && (
              <Chip icon={<ClockIcon />} onClear={() => setTime(null)}>
                at {formatTimecode(time)}
              </Chip>
            )}
            {pin && (
              <Chip icon={<MapPinIcon />} onClear={onClearPin}>
                Pinned to the image
              </Chip>
            )}
          </div>
        )}
        <Textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          onFocus={compose}
          onKeyDown={onKey}
          maxLength={5000}
          rows={2}
          placeholder={pinnable && !pin ? "Add a comment, or click the image to pin one" : "Add a comment"}
          className="min-h-16 resize-none"
        />
        <div className="flex flex-wrap items-center gap-2">
          {allowInternal && (
            <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Switch checked={internal} onCheckedChange={setInternal} size="sm" /> Team only
            </label>
          )}
          <div className="flex-1" />
          <Button type="submit" size="sm" disabled={!body.trim() || needName || send.isPending}>
            {send.isPending && <Spinner />} Comment
          </Button>
        </div>
        {canDecide && (
          <div className="grid grid-cols-2 gap-2 pt-1">
            <Button type="button" variant="outline" size="sm" disabled={send.isPending} onClick={() => decide("changes")}>
              <RotateCcwIcon /> Request changes
            </Button>
            <Button type="button" size="sm" disabled={send.isPending} onClick={() => decide("approved")} className="bg-emerald-600 text-white hover:bg-emerald-600/90">
              <CheckIcon /> Approve
            </Button>
          </div>
        )}
      </form>
    </div>
  )
}

function Chip({ icon, children, onClear }: { icon: React.ReactNode; children: React.ReactNode; onClear?: () => void }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md bg-secondary px-1.5 py-0.5 text-xs [&>svg]:size-3">
      {icon}
      {children}
      {onClear && (
        <button type="button" onClick={onClear} aria-label="Remove" className="text-muted-foreground hover:text-foreground">
          <XIcon className="size-3" />
        </button>
      )}
    </span>
  )
}

function CommentItem({
  c,
  number,
  active,
  onFocus,
  onSeek,
  onReply,
  onResolve,
  onRemove,
}: {
  c: Comment
  number?: number
  active?: boolean
  onFocus?: () => void
  onSeek?: (t: number) => void
  onReply?: () => void
  onResolve?: () => void
  onRemove?: () => void
}) {
  return (
    <div
      className={cn("group rounded-lg px-2 py-1.5 transition-colors", active ? "bg-brand/10" : "hover:bg-muted/60", c.resolved && "opacity-60")}
      onClick={onFocus}
    >
      <div className="flex items-center gap-1.5 text-xs">
        {number !== undefined && c.x != null && (
          <span className="flex size-4 items-center justify-center rounded-full bg-amber-500 text-[0.625rem] font-semibold text-white">{number}</span>
        )}
        <span className="truncate font-medium">{c.author}</span>
        {c.guest && <span className="rounded bg-secondary px-1 text-[0.625rem] text-muted-foreground">Guest</span>}
        {c.internal && (
          <span className="inline-flex items-center gap-0.5 rounded bg-secondary px-1 text-[0.625rem] text-muted-foreground">
            <EyeOffIcon className="size-2.5" /> Team only
          </span>
        )}
        <span className="text-muted-foreground">· {relativeTime(c.createdAt)}</span>
        <div className="ml-auto flex items-center opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
          {onReply && (
            <IconAction label="Reply" onClick={onReply}>
              <ReplyIcon />
            </IconAction>
          )}
          {onResolve && (
            <IconAction label={c.resolved ? "Reopen" : "Resolve"} onClick={onResolve}>
              {c.resolved ? <CircleDotIcon /> : <CheckIcon />}
            </IconAction>
          )}
          {onRemove && (
            <IconAction label="Delete" onClick={onRemove}>
              <Trash2Icon />
            </IconAction>
          )}
        </div>
      </div>
      <p className="mt-0.5 text-sm break-words whitespace-pre-wrap">
        {c.timecode != null && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              onSeek?.(c.timecode!)
            }}
            className="mr-1.5 rounded bg-brand/10 px-1 font-mono text-xs text-brand tabular-nums hover:bg-brand/20"
          >
            {formatTimecode(c.timecode)}
          </button>
        )}
        {c.body}
      </p>
    </div>
  )
}

function IconAction({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={(e) => {
        e.stopPropagation()
        onClick()
      }}
      className="flex size-6 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground [&_svg]:size-3.5"
    >
      {children}
    </button>
  )
}

function ReplyBox({ busy, disabled, onSend, onCancel }: { busy: boolean; disabled?: boolean; onSend: (t: string) => Promise<void>; onCancel: () => void }) {
  const [text, setText] = useState("")
  return (
    <form
      className="ml-6 flex gap-1.5 py-1"
      onSubmit={async (e) => {
        e.preventDefault()
        if (text.trim()) await onSend(text.trim())
      }}
    >
      <Input autoFocus value={text} onChange={(e) => setText(e.target.value)} placeholder="Reply" className="h-8" onKeyDown={(e) => e.key === "Escape" && onCancel()} />
      <Button type="submit" size="sm" disabled={busy || disabled || !text.trim()}>
        Reply
      </Button>
    </form>
  )
}
