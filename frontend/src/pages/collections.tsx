import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { LibraryIcon, LinkIcon, PlusIcon } from "lucide-react"
import { Link, useNavigate } from "react-router"
import { toast } from "sonner"

import { NameDialog } from "@/components/dialogs"
import { FileTile } from "@/components/file-icon"
import { Page } from "@/components/page"
import { EmptyState } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import { relativeTime } from "@/lib/format"

export default function CollectionsPage() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const cols = useQuery({ queryKey: ["collections"], queryFn: api.collections })
  const [creating, setCreating] = useState(false)

  return (
    <Page
      title="Collections"
      description="Hand-picked files and folders from anywhere, shared with one link. Nothing is moved or copied."
      actions={
        <Button onClick={() => setCreating(true)}>
          <PlusIcon /> <span className="hidden sm:inline">New collection</span>
        </Button>
      }
    >
      {cols.isLoading ? (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-28 rounded-xl" />
          ))}
        </div>
      ) : !cols.data?.length ? (
        <div className="rounded-xl border border-dashed bg-card/50 py-8">
          <EmptyState
            icon={<LibraryIcon />}
            title="No collections yet"
            description="Right-click files or folders and choose “Add to collection…”, or start one here."
          >
            <Button onClick={() => setCreating(true)}>
              <PlusIcon /> New collection
            </Button>
          </EmptyState>
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
          {cols.data.map((c) => (
            <Link key={c.id} to={`/collections/${c.id}`} className="group flex flex-col gap-3 rounded-xl border bg-card p-4 transition-colors hover:border-ring/50">
              <div className="flex -space-x-2">
                {c.items.slice(0, 4).map((it) => (
                  <FileTile key={it.place + it.path} entry={it} size="sm" className="bg-card ring-2 ring-card" />
                ))}
                {c.items.length === 0 && <FileTile entry={{ name: c.name, isDir: true }} size="sm" />}
              </div>
              <div className="min-w-0">
                <div className="truncate text-sm font-semibold">{c.name}</div>
                <div className="truncate text-xs text-muted-foreground">
                  {c.items.length} {c.items.length === 1 ? "item" : "items"} · updated {relativeTime(c.updatedAt)}
                </div>
              </div>
              {c.links > 0 && (
                <span className="flex items-center gap-1 text-xs text-brand">
                  <LinkIcon className="size-3.5" /> {c.links} {c.links === 1 ? "link" : "links"}
                </span>
              )}
            </Link>
          ))}
        </div>
      )}
      <NameDialog
        open={creating}
        title="New collection"
        label="Name"
        initial=""
        submitLabel="Create"
        onClose={() => setCreating(false)}
        onSubmit={async (name) => {
          try {
            const c = await api.createCollection({ name })
            qc.invalidateQueries({ queryKey: ["collections"] })
            navigate(`/collections/${c.id}`)
          } catch (e) {
            toast.error((e as Error).message)
            throw e
          }
        }}
      />
    </Page>
  )
}
