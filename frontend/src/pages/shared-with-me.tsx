import { useQueryClient } from "@tanstack/react-query"
import { LogOutIcon, MoreVerticalIcon, PencilIcon, ShareIcon, UsersIcon } from "lucide-react"
import { useState } from "react"
import { Link } from "react-router"
import { toast } from "sonner"

import { usePlaces } from "@/components/app-layout"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { api, type Place } from "@/lib/api"
import { fileCount, filesUrl, formatBytes } from "@/lib/format"

export default function SharedWithMePage() {
  const qc = useQueryClient()
  const { data, isLoading } = usePlaces()
  const [leaving, setLeaving] = useState<Place | null>(null)
  const shared = data?.places.filter((p) => p.kind === "shared") ?? []

  const leave = async () => {
    if (!leaving) return
    try {
      await api.deletePlace(leaving.id)
      toast.success(`Left “${leaving.name}”`)
      qc.invalidateQueries({ queryKey: ["places"] })
    } catch (e) {
      toast.error((e as Error).message)
    }
    setLeaving(null)
  }

  return (
    <Page title="Shared with me" description="Folders other people have given you access to.">
      {isLoading ? (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-28 rounded-xl" />
          ))}
        </div>
      ) : shared.length === 0 ? (
        <EmptyState icon={<UsersIcon />} title="Nothing shared with you yet" description="When someone shares a folder with you, it appears here and in the sidebar." />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
          {shared.map((p) => (
            <div key={p.id} className="group flex flex-col gap-3 rounded-xl border bg-card p-4 transition-colors hover:border-ring/50">
              <div className="flex items-start gap-3">
                <Link to={filesUrl(p.id)} className="flex min-w-0 flex-1 items-center gap-3">
                  <FileTile entry={{ name: p.name, isDir: true }} size="lg" />
                  <div className="min-w-0">
                    <div className="truncate font-semibold">{p.name}</div>
                    <div className="truncate text-sm text-muted-foreground">Shared by {p.grantedBy}</div>
                  </div>
                </Link>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="icon-sm" aria-label="Actions" className="text-muted-foreground">
                      <MoreVerticalIcon />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem asChild>
                      <Link to={filesUrl(p.id)}>Open</Link>
                    </DropdownMenuItem>
                    <DropdownMenuItem variant="destructive" onSelect={() => setLeaving(p)}>
                      <LogOutIcon /> Leave
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
              <div className="flex flex-wrap items-center gap-1.5">
                <Badge variant="secondary">{p.canWrite ? "Can edit" : "Can view"}</Badge>
                {p.canDelete && <Badge variant="secondary">Can delete</Badge>}
                {p.canShare && (
                  <Badge variant="secondary">
                    <ShareIcon /> Can reshare
                  </Badge>
                )}
                {!p.canWrite && (
                  <Badge variant="outline">
                    <PencilIcon className="opacity-40" /> Read only
                  </Badge>
                )}
                <span className="ml-auto text-xs text-muted-foreground">{p.items ? `${formatBytes(p.size)} · ${fileCount(p.items)}` : ""}</span>
              </div>
            </div>
          ))}
        </div>
      )}
      <AlertDialog open={!!leaving} onOpenChange={(o) => !o && setLeaving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Leave “{leaving?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>You'll lose access until {leaving?.grantedBy} shares it again. Nothing is deleted.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={leave}>
              Leave folder
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}
