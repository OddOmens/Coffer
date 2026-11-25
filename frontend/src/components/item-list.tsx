import type { ReactNode } from "react"
import { Link } from "react-router"

import { FileTile } from "@/components/file-icon"
import type { Item } from "@/lib/api"
import { filesUrl, formatBytes, parentPath, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

/** Where an item opens: its folder, or its parent folder with the preview. */
export function itemUrl(it: Pick<Item, "place" | "path" | "isDir" | "name">) {
  if (it.isDir) return filesUrl(it.place, it.path)
  return `${filesUrl(it.place, parentPath(it.path))}?open=${encodeURIComponent(it.name)}`
}

/** A list of items from different places (collections, tags, favourites). */
export function ItemList({ items, actions, empty }: { items: Item[]; actions?: (it: Item) => ReactNode; empty?: ReactNode }) {
  if (!items.length) return <>{empty}</>
  return (
    <div className="overflow-hidden rounded-xl border bg-card">
      {items.map((it) => (
        <div key={it.place + it.path} className={cn("flex items-center gap-3 border-b px-4 py-[var(--row-y)] last:border-b-0 hover:bg-muted/50", it.missing && "opacity-60")}>
          <FileTile entry={it} />
          <Link to={itemUrl(it)} className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium hover:underline">{it.name}</div>
            <div className="truncate text-xs text-muted-foreground">
              {it.missing ? "Missing — moved or deleted outside Coffer" : `${it.placeName}${parentPath(it.path) === "/" ? "" : parentPath(it.path)}`}
              {!it.missing && it.size >= 0 && <> · {formatBytes(it.size)}</>}
              {!it.missing && it.modTime > 0 && <> · {relativeTime(it.modTime)}</>}
            </div>
          </Link>
          {actions?.(it)}
        </div>
      ))}
    </div>
  )
}
