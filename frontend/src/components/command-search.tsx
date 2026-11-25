import { useEffect, useState, useSyncExternalStore } from "react"
import { SearchIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Hit } from "@/lib/api"
import { deferred } from "@/lib/deferred"
import { filesUrl, parentPath } from "@/lib/format"
import { whenIdle } from "@/lib/idle"
import { cn } from "@/lib/utils"

// Open state shared by every search button and the ⌘K shortcut.
let open = false
const subs = new Set<() => void>()
export const setSearchOpen = (v: boolean) => {
  open = v
  subs.forEach((f) => f())
}
export const openSearch = () => setSearchOpen(true)
export const useSearchOpen = () => useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => open)

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform)

export function CommandSearchButton({ className }: { className?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="Search" className={cn("text-muted-foreground hover:text-foreground", className)} onClick={openSearch}>
          <SearchIcon className="size-4" />
        </Button>
      </TooltipTrigger>
      <TooltipContent side="top">
        Search <Kbd className="ml-1">{isMac ? "⌘" : "Ctrl"} K</Kbd>
      </TooltipContent>
    </Tooltip>
  )
}

/** Opens a file from anywhere: its folder, with the preview open. */
export function hitUrl(h: Pick<Hit, "place" | "path" | "isDir" | "name">) {
  if (h.isDir) return filesUrl(h.place, h.path)
  return `${filesUrl(h.place, parentPath(h.path))}?open=${encodeURIComponent(h.name)}`
}

const SearchDialog = deferred(() => import("@/components/command-search-dialog").then((m) => m.CommandSearchDialog))

/**
 * The search dialog. Its code (and the list library under it) loads once
 * the page is idle, or the first time search is opened.
 */
export function CommandSearch() {
  const isOpen = useSearchOpen()
  const [idle, setIdle] = useState(false)
  useEffect(() => whenIdle(() => setIdle(true)), [])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        setSearchOpen(!open)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])
  return idle || isOpen ? <SearchDialog /> : null
}
