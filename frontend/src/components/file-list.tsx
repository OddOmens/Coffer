import { useEffect, useMemo, useRef, useState, type DragEvent, type MouseEvent, type ReactNode, type TouchEvent } from "react"
import {
  ArrowDownIcon,
  ArrowUpIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  CloudAlertIcon,
  CloudCheckIcon,
  CloudIcon,
  CloudUploadIcon,
  DatabaseBackupIcon,
  CircleAlertIcon,
  CornerDownRightIcon,
  LayersIcon,
  Link2Icon,
  LockIcon,
  MessageSquareIcon,
  MoreVerticalIcon,
  StarIcon,
} from "lucide-react"

import { ActionSheet } from "@/components/action-sheet"
import { FileTile } from "@/components/file-icon"
import { TagChip } from "@/components/tag-chip"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Pagination,
  PaginationContent,
  PaginationEllipsis,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination"
import { useIsMobile } from "@/hooks/use-mobile"
import type { BackupMark, Entry, Lock, ReviewSummary } from "@/lib/api"
import { describeEntry, formatDate, hasThumb, kindLabel, parentPath, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Stacked } from "@/lib/versions"

export type SortKey = "name" | "size" | "modTime"
export type Sort = { key: SortKey; dir: "asc" | "desc" }

export function sortEntries<T extends Entry>(entries: T[], sort: Sort) {
  const m = sort.dir === "asc" ? 1 : -1
  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" })
  return [...entries].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    if (sort.key === "name") return m * collator.compare(a.name, b.name)
    const d = sort.key === "size" ? a.size - b.size : a.modTime - b.modTime
    return d !== 0 ? m * d : collator.compare(a.name, b.name)
  })
}

export type Action = {
  label: string
  icon: ReactNode
  onSelect: () => void
  destructive?: boolean
  separator?: boolean
}

/** Stable key for an entry, unique across places in search results. */
export const keyOf = (e: Entry & { place?: string }) => (e.place ? e.place + ":" : "") + e.path

/** Click / shift-click / cmd-click selection over an ordered list. */
export function useSelection<T extends Entry>(entries: T[]) {
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const anchor = useRef<string | null>(null)

  const valid = useMemo(() => {
    const keys = new Set(entries.map(keyOf))
    return new Set([...selected].filter((p) => keys.has(p)))
  }, [entries, selected])

  const toggle = (k: string) => {
    anchor.current = k
    setSelected((s) => {
      const n = new Set(s)
      if (n.has(k)) n.delete(k)
      else n.add(k)
      return n
    })
  }

  const click = (e: T, ev: { shiftKey: boolean; metaKey: boolean; ctrlKey: boolean }) => {
    const k = keyOf(e)
    if (ev.shiftKey && anchor.current) {
      const a = entries.findIndex((x) => keyOf(x) === anchor.current)
      const b = entries.findIndex((x) => keyOf(x) === k)
      if (a >= 0 && b >= 0) {
        const [lo, hi] = a < b ? [a, b] : [b, a]
        setSelected(new Set(entries.slice(lo, hi + 1).map(keyOf)))
        return
      }
    }
    anchor.current = k
    if (ev.metaKey || ev.ctrlKey) toggle(k)
    else setSelected(new Set([k]))
  }

  return {
    selected: valid,
    items: entries.filter((e) => valid.has(keyOf(e))),
    click,
    toggle,
    selectOnly: (k: string) => {
      anchor.current = k
      setSelected(new Set([k]))
    },
    all: () => setSelected(new Set(entries.map(keyOf))),
    clear: () => setSelected(new Set()),
  }
}

type Props<T extends Entry> = {
  entries: T[]
  view: "list" | "grid"
  selection: ReturnType<typeof useSelection<T>>
  sort: Sort
  onSort: (s: Sort) => void
  onOpen: (e: T) => void
  thumbUrl: (e: T) => string
  actions?: (e: T) => Action[]
  onMove?: (paths: string[], dest: string) => void
  onDropFiles?: (dt: DataTransfer, dest: string) => void
  location?: (e: T) => string | undefined
  sharedPaths?: Set<string>
  pageSize?: number
  resetKey?: string
  tagColor?: (tag: string) => string | undefined
  onTag?: (tag: string) => void
  /** Version stacks: called with the newest version's entry. */
  onToggleStack?: (e: T) => void
  stackOpen?: (e: T) => boolean
}

export function lockText(l: Lock) {
  return l.source === "smb" ? `Open over SMB by ${l.username}` : `Locked by ${l.username}${l.note ? ` · ${l.note}` : ""}`
}

/** Review state as a small badge: approved, changes requested or open comments. */
// backupText says where the copies of an item are kept, for a tooltip.
function backupText(m: BackupMark) {
  const where = `${m.drive}${m.cloud ? " (cloud" + (m.encrypted ? ", encrypted)" : ")") : ""}`
  const when = m.last ? `, last run ${relativeTime(m.last)}` : ""
  switch (m.state) {
    case "done":
      return `Backed up to ${where} by “${m.backup}”${when}`
    case "pending":
      return `Not in the backup yet: “${m.backup}” copies it to ${where} on its next run`
    case "partial":
      return `Some folders inside are backed up to ${where}`
    default:
      return `In “${m.backup}” to ${where}, but its latest backup couldn't be checked just now`
  }
}

/** BackupBadge shows whether a backup holds an item, with the details on hover. */
export function BackupBadge({ mark, label, className }: { mark?: BackupMark; label?: boolean; className?: string }) {
  if (!mark) return null
  const Icon = !mark.cloud
    ? DatabaseBackupIcon
    : mark.state === "done"
      ? CloudCheckIcon
      : mark.state === "pending"
        ? CloudUploadIcon
        : mark.state === "unknown"
          ? CloudAlertIcon
          : CloudIcon
  const text = backupText(mark)
  const short = { done: "Backed up", pending: "Waiting for backup", partial: "Partly backed up", unknown: "Backup unchecked" }[mark.state]
  return (
    <span
      title={text}
      className={cn(
        "inline-flex shrink-0 items-center gap-1",
        mark.state === "done" ? "text-emerald-600 dark:text-emerald-400" : mark.state === "pending" ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground",
        className,
      )}
    >
      <Icon className="size-3.5" aria-label={text} />
      {label && (
        <span>
          {short} · {mark.drive}
        </span>
      )}
    </span>
  )
}

export function ReviewBadge({ review, className }: { review?: ReviewSummary; className?: string }) {
  if (!review) return null
  if (review.status === "approved")
    return (
      <span className={cn("inline-flex shrink-0 items-center gap-0.5 text-emerald-600 dark:text-emerald-400", className)} title={`Approved by ${review.by}`}>
        <CheckCircle2Icon className="size-3.5" /> Approved
      </span>
    )
  if (review.status === "changes")
    return (
      <span className={cn("inline-flex shrink-0 items-center gap-0.5 text-amber-600 dark:text-amber-400", className)} title={`Changes requested by ${review.by}`}>
        <CircleAlertIcon className="size-3.5" /> Changes
      </span>
    )
  if (!review.comments) return null
  return (
    <span className={cn("inline-flex shrink-0 items-center gap-0.5", className)} title={`${review.comments} comments, ${review.open} open`}>
      <MessageSquareIcon className="size-3.5" /> {review.comments}
    </span>
  )
}

const DRAG_TYPE = "application/x-coffer-paths"

/** Touch screens tap to open and long-press for actions, as in iOS Files. */
const touch = typeof window !== "undefined" && window.matchMedia("(pointer: coarse)").matches

/** Long-press on touch. `consume` tells the click that follows a long-press to do nothing. */
function useLongPress(onLong: () => void) {
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const start = useRef<{ x: number; y: number } | null>(null)
  const fired = useRef(false)
  const cancel = () => {
    clearTimeout(timer.current)
    start.current = null
  }
  useEffect(() => () => clearTimeout(timer.current), [])
  return {
    consume: () => {
      const f = fired.current
      fired.current = false
      return f
    },
    handlers: touch
      ? {
          onTouchStart: (ev: TouchEvent) => {
            fired.current = false
            start.current = { x: ev.touches[0].clientX, y: ev.touches[0].clientY }
            timer.current = setTimeout(() => {
              fired.current = true
              onLong()
            }, 450)
          },
          // Scrolling isn't a press.
          onTouchMove: (ev: TouchEvent) => {
            const s = start.current
            if (s && Math.hypot(ev.touches[0].clientX - s.x, ev.touches[0].clientY - s.y) > 8) cancel()
          },
          // Lifting the finger after a long-press would click whatever opened under it.
          onTouchEnd: (ev: TouchEvent) => {
            if (fired.current) ev.preventDefault()
            cancel()
          },
          onTouchCancel: cancel,
        }
      : {},
  }
}

/** A row or tile's touch behaviour: the phone action sheet, long-press, and what a tap does. */
function useTouchItem<T extends Entry>(props: Props<T> & { entry: T }) {
  const { entry, selection, onOpen, actions } = props
  const isMobile = useIsMobile()
  const [sheet, setSheet] = useState(false)
  // Tablets keep the context menu, which opens on its own long-press.
  const press = useLongPress(() => isMobile && !!actions?.(entry).length && setSheet(true))
  const k = keyOf(entry)
  return {
    isMobile,
    sheet,
    setSheet,
    handlers: { ...press.handlers, ...(isMobile && { onContextMenu: (ev: MouseEvent) => ev.preventDefault() }) },
    tap: (ev: { shiftKey: boolean; metaKey: boolean; ctrlKey: boolean }) => {
      if (!touch) return selection.click(entry, ev)
      // The finger lifting after a long-press isn't a tap.
      if (press.consume()) return
      if (selection.selected.size) selection.toggle(k)
      else onOpen(entry)
    },
  }
}

/** The phone action sheet for one item, with Select first. */
function ItemSheet<T extends Entry>({ entry, actions, selection, open, onOpenChange }: Props<T> & { entry: T; open: boolean; onOpenChange: (o: boolean) => void }) {
  if (!open) return null
  const k = keyOf(entry)
  const selected = selection.selected.has(k)
  const many = selected && selection.selected.size > 1
  const list = actions?.(entry) ?? []
  return (
    <ActionSheet
      open={open}
      onOpenChange={onOpenChange}
      icon={<FileTile entry={entry} size="sm" />}
      title={many ? `${selection.selected.size} items` : entry.name}
      description={many ? undefined : describeEntry(entry)}
      actions={[
        { label: selected ? "Deselect" : "Select", icon: <CheckCircle2Icon />, onSelect: () => selection.toggle(k) },
        ...list.map((a, i) => (i === 0 ? { ...a, separator: true } : a)),
      ]}
    />
  )
}

export function FileList<T extends Entry>(props: Props<T>) {
  const { entries, view, selection, pageSize = 50, resetKey } = props
  const [page, setPage] = useState(1)
  useEffect(() => setPage(1), [resetKey, pageSize])
  const pages = Math.max(1, Math.ceil(entries.length / pageSize))
  const cur = Math.min(page, pages)
  const shown = entries.slice((cur - 1) * pageSize, cur * pageSize)
  const allSelected = entries.length > 0 && selection.selected.size === entries.length

  return (
    <div>
      {view === "list" ? (
        <div role="table" className="overflow-hidden rounded-xl border bg-card">
          <div
            role="row"
            className={cn(
              "grid grid-cols-[1rem_1fr_2rem] items-center gap-3 border-b px-4 py-2 text-xs text-muted-foreground sm:grid-cols-[1rem_1fr_9rem_2rem] lg:grid-cols-[1rem_1fr_11rem_10rem_2rem]",
              touch && "max-sm:px-3",
              touch && !selection.selected.size && "max-sm:grid-cols-[1fr_2.25rem]",
            )}
          >
            <Checkbox
              aria-label="Select all"
              className={cn(touch && !selection.selected.size && "max-sm:hidden")}
              checked={allSelected ? true : selection.selected.size ? "indeterminate" : false}
              onCheckedChange={() => (allSelected ? selection.clear() : selection.all())}
            />
            <SortHeader label="File name" k="name" {...props} />
            <SortHeader label="Modified" k="modTime" className="hidden sm:flex" {...props} />
            <SortHeader label={props.location ? "Location" : "Type"} k="size" className="hidden lg:flex" {...props} />
            <span />
          </div>
          {shown.map((e) => (
            <Row key={keyOf(e)} entry={e} {...props} />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] gap-3">
          {shown.map((e) => (
            <Tile key={keyOf(e)} entry={e} {...props} />
          ))}
        </div>
      )}
      {pages > 1 && <Pager page={cur} pages={pages} onPage={setPage} />}
    </div>
  )
}

function Pager({ page, pages, onPage }: { page: number; pages: number; onPage: (p: number) => void }) {
  const nums: (number | "…")[] = []
  for (let i = 1; i <= pages; i++) {
    if (i === 1 || i === pages || Math.abs(i - page) <= 1) nums.push(i)
    else if (nums[nums.length - 1] !== "…") nums.push("…")
  }
  const go = (p: number) => (ev: MouseEvent) => {
    ev.preventDefault()
    onPage(Math.min(pages, Math.max(1, p)))
    window.scrollTo({ top: 0, behavior: "smooth" })
  }
  return (
    <Pagination className="mt-4">
      <PaginationContent>
        <PaginationItem>
          <PaginationPrevious href="#" onClick={go(page - 1)} aria-disabled={page === 1} className={cn(page === 1 && "pointer-events-none opacity-40")} />
        </PaginationItem>
        {nums.map((n, i) => (
          <PaginationItem key={i}>
            {n === "…" ? (
              <PaginationEllipsis />
            ) : (
              <PaginationLink href="#" isActive={n === page} onClick={go(n)}>
                {n}
              </PaginationLink>
            )}
          </PaginationItem>
        ))}
        <PaginationItem>
          <PaginationNext href="#" onClick={go(page + 1)} aria-disabled={page === pages} className={cn(page === pages && "pointer-events-none opacity-40")} />
        </PaginationItem>
      </PaginationContent>
    </Pagination>
  )
}

function SortHeader<T extends Entry>({ label, k, sort, onSort, className }: Props<T> & { label: string; k: "name" | "size" | "modTime"; className?: string }) {
  const active = sort.key === k
  return (
    <button
      type="button"
      className={cn("flex items-center gap-1 text-left hover:text-foreground", active && "text-foreground", className)}
      onClick={() => onSort({ key: k, dir: active && sort.dir === "asc" ? "desc" : "asc" })}
    >
      {label}
      {active && (sort.dir === "asc" ? <ArrowUpIcon className="size-3" /> : <ArrowDownIcon className="size-3" />)}
    </button>
  )
}

function useDnd<T extends Entry>({ entry, selection, onMove, onDropFiles }: Props<T> & { entry: T }) {
  const [over, setOver] = useState(false)
  const acceptsDrop = entry.isDir && (onMove || onDropFiles)
  return {
    over,
    props: {
      draggable: !!onMove && !touch,
      onDragStart: (ev: DragEvent) => {
        const items = selection.selected.has(keyOf(entry)) ? selection.items : [entry]
        ev.dataTransfer.setData(DRAG_TYPE, JSON.stringify(items.map((e) => e.path)))
        ev.dataTransfer.effectAllowed = "move"
      },
      onDragOver: (ev: DragEvent) => {
        if (!acceptsDrop) return
        const internal = ev.dataTransfer.types.includes(DRAG_TYPE)
        if ((internal && onMove) || (!internal && ev.dataTransfer.types.includes("Files") && onDropFiles)) {
          ev.preventDefault()
          ev.stopPropagation()
          ev.dataTransfer.dropEffect = internal ? "move" : "copy"
          setOver(true)
        }
      },
      onDragLeave: () => setOver(false),
      onDrop: (ev: DragEvent) => {
        if (!acceptsDrop) return
        setOver(false)
        const raw = ev.dataTransfer.getData(DRAG_TYPE)
        ev.preventDefault()
        ev.stopPropagation()
        if (raw) {
          const paths = (JSON.parse(raw) as string[]).filter((p) => p !== entry.path)
          if (paths.length) onMove?.(paths, entry.path)
        } else {
          onDropFiles?.(ev.dataTransfer, entry.path)
        }
      },
    },
  }
}

function renderActions(list: Action[], Item: typeof DropdownMenuItem | typeof ContextMenuItem, Sep: typeof DropdownMenuSeparator | typeof ContextMenuSeparator) {
  return list.map((a) => [
    a.separator && <Sep key={a.label + "-sep"} />,
    <Item key={a.label} onSelect={a.onSelect} variant={a.destructive ? "destructive" : "default"}>
      {a.icon}
      {a.label}
    </Item>,
  ])
}

function ItemMenu<T extends Entry>({ entry, actions, selection, children, phone }: Props<T> & { entry: T; children: ReactNode; phone?: boolean }) {
  const list = actions?.(entry) ?? []
  if (!list.length || phone) return <>{children}</>
  return (
    <ContextMenu
      onOpenChange={(open) => {
        if (open && !selection.selected.has(keyOf(entry))) selection.selectOnly(keyOf(entry))
      }}
    >
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent className="w-56">{renderActions(list, ContextMenuItem, ContextMenuSeparator)}</ContextMenuContent>
    </ContextMenu>
  )
}

function DotsMenu<T extends Entry>({ entry, actions, selection, onSheet }: Props<T> & { entry: T; onSheet?: () => void }) {
  const list = actions?.(entry) ?? []
  if (!list.length) return null
  if (onSheet)
    return (
      <Button
        variant="ghost"
        size="icon"
        aria-label="Actions"
        className="-mr-1 text-muted-foreground"
        onClick={(e) => {
          e.stopPropagation()
          onSheet()
        }}
      >
        <MoreVerticalIcon />
      </Button>
    )
  return (
    <DropdownMenu
      onOpenChange={(open) => {
        if (open && !selection.selected.has(keyOf(entry))) selection.selectOnly(keyOf(entry))
      }}
    >
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label="Actions" onClick={(e) => e.stopPropagation()} className="text-muted-foreground">
          <MoreVerticalIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        {renderActions(list, DropdownMenuItem, DropdownMenuSeparator)}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function Row<T extends Entry>(props: Props<T> & { entry: T }) {
  const { entry, selection, onOpen, location, sharedPaths } = props
  const stacked = entry as Stacked<T>
  const dnd = useDnd(props)
  const t = useTouchItem(props)
  const selected = selection.selected.has(keyOf(entry))
  return (
    <>
      <ItemMenu {...props} phone={t.isMobile}>
        <div
          role="row"
          aria-selected={selected}
          {...dnd.props}
          {...t.handlers}
          onClick={(ev) => t.tap(ev)}
          onDoubleClick={() => !touch && onOpen(entry)}
          className={cn(
            "group grid cursor-default grid-cols-[1rem_1fr_2rem] items-center gap-3 border-b px-4 py-[var(--row-y)] select-none last:border-b-0 hover:bg-muted/50 sm:grid-cols-[1rem_1fr_9rem_2rem] lg:grid-cols-[1rem_1fr_11rem_10rem_2rem]",
            // Phones: checkboxes only once something is selected, and a pressed state.
            touch && "transition-colors active:bg-muted max-sm:px-3 max-sm:py-2",
            touch && !selection.selected.size && "max-sm:grid-cols-[1fr_2.25rem]",
            selected && "bg-brand/[0.06] hover:bg-brand/10",
            dnd.over && "bg-brand/15 ring-2 ring-brand ring-inset",
          )}
        >
          <Checkbox
            aria-label={`Select ${entry.name}`}
            className={cn(touch && !selection.selected.size && "max-sm:hidden")}
            checked={selected}
            onClick={(ev) => ev.stopPropagation()}
            onCheckedChange={() => selection.toggle(keyOf(entry))}
          />
          <div className={cn("flex min-w-0 items-center gap-3", stacked.stackChild && "pl-5")}>
            {stacked.stackChild && <CornerDownRightIcon className="-ml-5 size-3.5 shrink-0 text-muted-foreground" />}
            <Thumb {...props} small />
            <div className="min-w-0">
              <button
                type="button"
                className="flex max-w-full items-center gap-1.5 text-left text-sm font-medium hover:underline max-sm:text-[0.9375rem]"
                onClick={(ev) => {
                  // On touch the whole row is the target.
                  if (touch) return
                  ev.stopPropagation()
                  onOpen(entry)
                }}
                title={entry.name}
              >
                <span className="truncate">{entry.name}</span>
                {entry.favorite && <StarIcon className="size-3.5 shrink-0 fill-amber-400 text-amber-400" aria-label="Favourite" />}
                {sharedPaths?.has(entry.path) && <Link2Icon className="size-3.5 shrink-0 text-brand" aria-label="Has a share link" />}
                <BackupBadge mark={entry.backup} />
                {entry.lock && (
                  <span title={lockText(entry.lock)} className="shrink-0">
                    <LockIcon className={cn("size-3.5", entry.lock.source === "smb" ? "text-sky-500" : "text-amber-600")} aria-label={lockText(entry.lock)} />
                  </span>
                )}
              </button>
              <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground tabular-nums">
                <span className="max-w-full truncate">{describeEntry(entry)}</span>
                {stacked.older && props.onToggleStack && (
                  <button
                    type="button"
                    className="inline-flex shrink-0 items-center gap-0.5 rounded bg-secondary px-1 font-medium text-secondary-foreground hover:bg-accent"
                    onClick={(ev) => {
                      ev.stopPropagation()
                      props.onToggleStack!(entry)
                    }}
                    aria-expanded={props.stackOpen?.(entry)}
                  >
                    <LayersIcon className="size-3" /> v{stacked.version} · {stacked.older.length + 1} versions
                    <ChevronRightIcon className={cn("size-3 transition-transform", props.stackOpen?.(entry) && "rotate-90")} />
                  </button>
                )}
                <ReviewBadge review={entry.review} />
                {entry.lock && <span className="hidden truncate text-amber-700 sm:inline dark:text-amber-400">{lockText(entry.lock)}</span>}
                {entry.tags?.slice(0, 3).map((t) => (
                  <TagChip key={t} tag={t} color={props.tagColor?.(t)} onClick={props.onTag ? () => props.onTag!(t) : undefined} />
                ))}
                {(entry.tags?.length ?? 0) > 3 && <span>+{entry.tags!.length - 3}</span>}
              </div>
            </div>
          </div>
          <div className="hidden text-[0.8125rem] text-muted-foreground tabular-nums sm:block" title={formatDate(entry.modTime)}>
            {entry.modTime ? relativeTime(entry.modTime) : "—"}
          </div>
          <div className="hidden truncate text-[0.8125rem] text-muted-foreground lg:block">
            {location ? location(entry) : kindLabel(entry)}
          </div>
          <div className="flex justify-end">
            <DotsMenu {...props} onSheet={t.isMobile ? () => t.setSheet(true) : undefined} />
          </div>
        </div>
      </ItemMenu>
      {/* Outside the row: clicks in a portal still bubble through React parents. */}
      <ItemSheet {...props} open={t.sheet} onOpenChange={t.setSheet} />
    </>
  )
}

function Tile<T extends Entry>(props: Props<T> & { entry: T }) {
  const { entry, selection, onOpen, sharedPaths } = props
  const dnd = useDnd(props)
  const t = useTouchItem(props)
  const selected = selection.selected.has(keyOf(entry))
  return (
    <>
      <ItemMenu {...props} phone={t.isMobile}>
        <div
          {...dnd.props}
          {...t.handlers}
          aria-selected={selected}
          onClick={(ev) => t.tap(ev)}
          onDoubleClick={() => !touch && onOpen(entry)}
          className={cn(
            "group relative flex cursor-default flex-col overflow-hidden rounded-xl border bg-card select-none hover:border-ring/40",
            touch && "transition-transform duration-150 active:scale-[0.97]",
            selected && "border-brand ring-2 ring-brand/25",
            dnd.over && "border-brand bg-brand/10",
          )}
        >
          <div className="relative flex aspect-[4/3] items-center justify-center overflow-hidden bg-muted/50">
            <Thumb {...props} />
          </div>
          <div className="flex items-start gap-1 p-2.5">
            <div className="min-w-0 flex-1">
              <button
                type="button"
                className="flex max-w-full items-center gap-1 text-left text-sm font-medium hover:underline"
                title={entry.name}
                onClick={(ev) => {
                  if (touch) return
                  ev.stopPropagation()
                  onOpen(entry)
                }}
              >
                <span className="truncate">{entry.name}</span>
                {entry.favorite && <StarIcon className="size-3.5 shrink-0 fill-amber-400 text-amber-400" />}
                {sharedPaths?.has(entry.path) && <Link2Icon className="size-3.5 shrink-0 text-brand" />}
                <BackupBadge mark={entry.backup} />
                {entry.lock && <LockIcon className="size-3.5 shrink-0 text-amber-600" aria-label={lockText(entry.lock)} />}
              </button>
              <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                <span className="truncate">{(entry as Stacked<T>).older ? `v${(entry as Stacked<T>).version} · ${(entry as Stacked<T>).older!.length + 1} versions` : describeEntry(entry)}</span>
                <ReviewBadge review={entry.review} />
              </div>
            </div>
            <div className="-mr-1.5 -mt-1">
              <DotsMenu {...props} onSheet={t.isMobile ? () => t.setSheet(true) : undefined} />
            </div>
          </div>
          <div className={cn("absolute top-2.5 left-2.5 opacity-0 group-hover:opacity-100 group-aria-selected:opacity-100", touch && selection.selected.size > 0 && "opacity-100")}>
            <Checkbox
              className="bg-background"
              checked={selected}
              onClick={(ev) => ev.stopPropagation()}
              onCheckedChange={() => selection.toggle(keyOf(entry))}
            />
          </div>
        </div>
      </ItemMenu>
      <ItemSheet {...props} open={t.sheet} onOpenChange={t.setSheet} />
    </>
  )
}

function Thumb<T extends Entry>({ entry, thumbUrl, small }: Props<T> & { entry: T; small?: boolean }) {
  const [failed, setFailed] = useState(false)
  if (hasThumb(entry) && !failed) {
    return (
      <img
        src={thumbUrl(entry)}
        alt=""
        loading="lazy"
        decoding="async"
        draggable={false}
        onError={() => setFailed(true)}
        className={small ? "size-9 shrink-0 rounded-lg border object-cover" : "absolute inset-0 size-full object-cover"}
      />
    )
  }
  return small ? <FileTile entry={entry} /> : <FileTile entry={entry} size="lg" />
}

/** Horizontal card used for recent / suggested files. */
export function FileCard<T extends Entry>({
  entry,
  onOpen,
  actions,
  subtitle,
}: {
  entry: T
  onOpen: () => void
  actions?: Action[]
  subtitle?: string
}) {
  const isMobile = useIsMobile()
  const [sheet, setSheet] = useState(false)
  const press = useLongPress(() => isMobile && !!actions?.length && setSheet(true))
  return (
    <div className="group flex min-w-0 items-center gap-3 rounded-xl border bg-card p-3 transition-colors hover:border-ring/40">
      <button
        type="button"
        {...(isMobile && press.handlers)}
        onClick={() => !press.consume() && onOpen()}
        className={cn("flex min-w-0 flex-1 items-center gap-3 text-left select-none", touch && "transition-opacity active:opacity-60")}
      >
        <FileTile entry={entry} />
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{entry.name}</div>
          <div className="truncate text-xs text-muted-foreground">{subtitle ?? describeEntry(entry)}</div>
        </div>
      </button>
      {actions && actions.length > 0 && isMobile && (
        <>
          <Button variant="ghost" size="icon" className="-mr-1 self-start text-muted-foreground" aria-label="Actions" onClick={() => setSheet(true)}>
            <MoreVerticalIcon />
          </Button>
          <ActionSheet open={sheet} onOpenChange={setSheet} icon={<FileTile entry={entry} size="sm" />} title={entry.name} description={subtitle ?? describeEntry(entry)} actions={actions} />
        </>
      )}
      {actions && actions.length > 0 && !isMobile && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" className="-mr-1 self-start text-muted-foreground" aria-label="Actions">
              <MoreVerticalIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            {renderActions(actions, DropdownMenuItem, DropdownMenuSeparator)}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  )
}

export { parentPath }
