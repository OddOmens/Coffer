import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { RotateCcwIcon, Trash2Icon } from "lucide-react"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { FileTile } from "@/components/file-icon"
import { Page } from "@/components/page"
import { EmptyState } from "@/components/states"
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
import { Skeleton } from "@/components/ui/skeleton"
import { api, type TrashItem } from "@/lib/api"
import { filesUrl, formatBytes, parentPath, relativeTime } from "@/lib/format"

export default function TrashPage() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const { data, isLoading } = useQuery({ queryKey: ["trash"], queryFn: api.trash })
  const [confirm, setConfirm] = useState<TrashItem | "all" | null>(null)

  const done = () => {
    qc.invalidateQueries({ queryKey: ["trash"] })
    qc.invalidateQueries({ queryKey: ["list"] })
    qc.invalidateQueries({ queryKey: ["places"] })
  }

  const restore = async (it: TrashItem) => {
    try {
      const r = await api.restore(it.id)
      toast.success(`Restored “${it.name}”`, {
        action: { label: "Show", onClick: () => navigate(filesUrl(r.place, parentPath(r.path))) },
      })
      done()
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  const purge = async () => {
    try {
      if (confirm === "all") {
        const r = await api.emptyTrash()
        const left = r.offline + r.failed
        if (left) {
          const why = [
            r.offline && `${r.offline} on a drive that's offline`,
            r.failed && `${r.failed} couldn't be removed`,
          ].filter(Boolean)
          toast.warning(`${left} item${left === 1 ? " was" : "s were"} left in the trash`, { description: `${why.join(", ")}. Try again later.` })
        } else toast.success(`Deleted ${r.deleted} item${r.deleted === 1 ? "" : "s"} for good`)
      } else if (confirm) await api.purge(confirm.id)
      done()
    } catch (e) {
      toast.error((e as Error).message)
    }
    setConfirm(null)
  }

  const total = (data ?? []).reduce((n, it) => n + it.size, 0)
  const others = (data ?? []).filter((it) => !it.mine)
  const otherPeople = [...new Set(others.map((it) => it.deletedBy).filter(Boolean))]

  return (
    <Page
      title="Deleted files"
      description={data?.length ? `${data.length} items · ${formatBytes(total)}. Items are removed for good after the retention period.` : "Deleted items wait here before they are removed for good."}
      actions={
        !!data?.length && (
          <Button variant="outline" size="lg" onClick={() => setConfirm("all")}>
            <Trash2Icon /> Empty trash
          </Button>
        )
      }
    >
      {isLoading ? (
        <Skeleton className="h-64 w-full rounded-xl" />
      ) : !data?.length ? (
        <EmptyState icon={<Trash2Icon />} title="Trash is empty" description="Files deleted in Coffer or over WebDAV land here." />
      ) : (
        <div className="overflow-hidden rounded-xl border bg-card">
          {data.map((it) => (
            <div key={it.id} className="flex items-center gap-3 border-b px-4 py-[var(--row-y)] last:border-b-0">
              <FileTile entry={it} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{it.name}</div>
                <div className="truncate text-sm text-muted-foreground">
                  {it.placeName}
                  {parentPath(it.path) !== "/" && ` · ${parentPath(it.path)}`}
                </div>
              </div>
              <div className="hidden w-48 text-sm text-muted-foreground md:block">
                {relativeTime(it.deletedAt)}
                {it.deletedBy && ` by ${it.deletedBy}`}
              </div>
              <div className="hidden w-24 text-right text-sm text-muted-foreground tabular-nums sm:block">{it.isDir && !it.size ? "Folder" : formatBytes(it.size)}</div>
              <Button variant="outline" size="sm" onClick={() => restore(it)}>
                <RotateCcwIcon /> <span className="hidden sm:inline">Restore</span>
              </Button>
              <Button variant="ghost" size="icon-sm" onClick={() => setConfirm(it)} aria-label="Delete forever">
                <Trash2Icon />
              </Button>
            </div>
          ))}
        </div>
      )}
      <AlertDialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm === "all" ? "Empty the trash?" : `Delete “${confirm?.name}” forever?`}</AlertDialogTitle>
            <AlertDialogDescription>
              {confirm === "all" && others.length > 0 &&
                `This includes ${others.length} item${others.length === 1 ? "" : "s"} deleted by ${otherPeople.length && otherPeople.length <= 3 ? otherPeople.join(", ") : "other people"} in folders you share. `}
              {confirm !== "all" && confirm && !confirm.mine && `${confirm.deletedBy || "Someone else"} deleted this. `}
              This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={purge}>
              Delete forever
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}
