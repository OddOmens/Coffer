import { useEffect, useRef, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { FolderOpenIcon, HomeIcon, LinkIcon, Loader2Icon, Trash2Icon, UserRoundIcon, UsersIcon } from "lucide-react"
import { useNavigate } from "react-router"

import { hitUrl, setSearchOpen, useSearchOpen } from "@/components/command-search"
import { FileTile } from "@/components/file-icon"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { api, type Hit } from "@/lib/api"
import { describeEntry, kindFilters, parentPath } from "@/lib/format"
import { cn } from "@/lib/utils"

/** The search dialog itself; components/command-search.tsx loads it when it's needed. */
export function CommandSearchDialog() {
  const isOpen = useSearchOpen()
  const navigate = useNavigate()
  const [q, setQ] = useState("")
  const [kind, setKind] = useState("all")
  const [hits, setHits] = useState<Hit[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [ms, setMs] = useState(0)
  const ctl = useRef<AbortController | null>(null)
  const recent = useQuery({ queryKey: ["recent", 8], queryFn: () => api.recent(8), enabled: isOpen })

  // Results update as you type: each keystroke cancels the previous query.
  useEffect(() => {
    const kinds = kindFilters.find((k) => k.id === kind)?.kinds ?? []
    if (!q.trim() && !kinds.length) {
      setHits(null)
      return
    }
    ctl.current?.abort()
    const c = new AbortController()
    ctl.current = c
    setBusy(true)
    const t = setTimeout(() => {
      api
        .search(q, { kinds, limit: 40 }, c.signal)
        .then((r) => {
          setHits(r.entries)
          setMs(r.ms)
        })
        .catch(() => {})
        .finally(() => !c.signal.aborted && setBusy(false))
    }, 90)
    return () => {
      clearTimeout(t)
      c.abort()
    }
  }, [q, kind])

  const go = (url: string) => {
    setSearchOpen(false)
    setQ("")
    navigate(url)
  }

  const list = hits ?? recent.data?.entries ?? []
  return (
    <Dialog open={isOpen} onOpenChange={setSearchOpen}>
      <DialogContent
        mobile="center"
        className="overflow-hidden p-0 max-sm:top-[calc(env(safe-area-inset-top)+0.5rem)] max-sm:translate-y-0 sm:top-[12%] sm:max-w-2xl sm:translate-y-0"
        showCloseButton={false}
      >
        <DialogTitle className="sr-only">Search</DialogTitle>
        <DialogDescription className="sr-only">Search files and folders across your drives</DialogDescription>
        <Command shouldFilter={false} className="[&_[cmdk-input-wrapper]]:h-14">
          <CommandInput value={q} onValueChange={setQ} placeholder="Search files and folders…" className="h-12 text-base" />
          <div className="flex gap-1 overflow-x-auto border-b px-3 py-2">
            {kindFilters.map((k) => (
              <button
                key={k.id}
                type="button"
                onClick={() => setKind(k.id)}
                className={cn(
                  "shrink-0 rounded-md px-2.5 py-1 text-xs font-medium text-muted-foreground hover:bg-muted",
                  kind === k.id && "bg-secondary text-foreground ring-1 ring-border",
                )}
              >
                {k.label}
              </button>
            ))}
          </div>
          <CommandList className="max-h-[min(60vh,28rem)]">
            {hits && !busy && <CommandEmpty>No files match “{q}”.</CommandEmpty>}
            {!q && kind === "all" && (
              <CommandGroup heading="Go to">
                <CommandItem onSelect={() => go("/home")}>
                  <HomeIcon /> Home
                </CommandItem>
                <CommandItem onSelect={() => go("/files")}>
                  <FolderOpenIcon /> All files
                </CommandItem>
                <CommandItem onSelect={() => go("/shared")}>
                  <UsersIcon /> Shared with me
                </CommandItem>
                <CommandItem onSelect={() => go("/shares")}>
                  <LinkIcon /> Shared links
                </CommandItem>
                <CommandItem onSelect={() => go("/trash")}>
                  <Trash2Icon /> Deleted files
                </CommandItem>
                <CommandItem onSelect={() => go("/account")}>
                  <UserRoundIcon /> User settings
                </CommandItem>
              </CommandGroup>
            )}
            {list.length > 0 && (
              <CommandGroup
                heading={
                  hits ? (
                    <span className="flex items-center gap-2">
                      {hits.length} result{hits.length === 1 ? "" : "s"}
                      <span className="font-normal text-muted-foreground/70">in {ms} ms</span>
                      {busy && <Loader2Icon className="size-3 animate-spin" />}
                    </span>
                  ) : (
                    "Recently changed"
                  )
                }
              >
                {list.map((h) => (
                  <CommandItem key={h.place + h.path} value={h.place + h.path} onSelect={() => go(hitUrl(h))} className="gap-3 py-2">
                    <FileTile entry={h} size="sm" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-medium">{h.name}</div>
                      <div className="truncate text-xs text-muted-foreground">
                        {h.placeName}
                        {parentPath(h.path) !== "/" && ` · ${parentPath(h.path)}`} · {describeEntry(h)}
                      </div>
                    </div>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  )
}
