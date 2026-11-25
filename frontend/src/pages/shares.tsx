import { useMemo, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  CheckIcon,
  CopyIcon,
  FolderOpenIcon,
  KeyRoundIcon,
  LinkIcon,
  MoreVerticalIcon,
  PauseIcon,
  PencilIcon,
  PlayIcon,
  SearchIcon,
  Trash2Icon,
} from "lucide-react"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { useUser } from "@/components/app-layout"
import { FileTile } from "@/components/file-icon"
import { Page } from "@/components/page"
import { ShareDialog, useCopy } from "@/components/share-dialog"
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
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { api, type Share } from "@/lib/api"
import { filesUrl, formatDay, parentPath, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

const tabs = [
  { id: "active", label: "Active" },
  { id: "paused", label: "Paused" },
  { id: "expired", label: "Expired" },
  { id: "all", label: "All" },
] as const

function status(s: Share) {
  if (s.missing) return "missing"
  if (s.disabled) return "paused"
  if (!s.active) return "expired"
  return "active"
}

export default function SharesPage() {
  const me = useUser()
  const qc = useQueryClient()
  const [all, setAll] = useState(false)
  const [tab, setTab] = useState<(typeof tabs)[number]["id"]>("active")
  const [q, setQ] = useState("")
  const [editing, setEditing] = useState<Share | null>(null)
  const [revoking, setRevoking] = useState<Share | null>(null)
  const { data, isLoading } = useQuery({ queryKey: ["shares", "page", all], queryFn: () => api.shares({ all }) })

  const list = useMemo(() => {
    const needle = q.toLowerCase()
    return (data ?? []).filter((s) => {
      const st = status(s)
      const inTab = tab === "all" || st === tab || (tab === "expired" && st === "missing")
      return inTab && (!needle || [s.name, s.title, s.note, s.path, s.owner].some((x) => x?.toLowerCase().includes(needle)))
    })
  }, [data, tab, q])

  const refresh = () => qc.invalidateQueries({ queryKey: ["shares"] })

  const revoke = async () => {
    if (!revoking) return
    try {
      await api.deleteShare(revoking.id)
      toast.success("Link deleted")
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    }
    setRevoking(null)
  }

  const counts = (id: string) => (data ?? []).filter((s) => id === "all" || status(s) === id || (id === "expired" && status(s) === "missing")).length

  return (
    <Page
      title="Shared links"
      description="Public links to your files and folders. Pausing or deleting a link stops it immediately."
      actions={
        me.role === "admin" && (
          <div className="flex items-center gap-2">
            <Switch id="all" checked={all} onCheckedChange={setAll} />
            <Label htmlFor="all">Everyone's</Label>
          </div>
        )
      }
    >
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="inline-flex w-fit gap-1.5">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => setTab(t.id)}
              className={cn(
                "h-7 rounded-full border bg-card px-3 text-[0.8125rem] font-medium text-muted-foreground hover:text-foreground",
                tab === t.id && "border-foreground bg-foreground text-background hover:text-background",
              )}
            >
              {t.label}
              <span className="ml-1.5 text-xs tabular-nums opacity-60">{counts(t.id)}</span>
            </button>
          ))}
        </div>
        <div className="relative sm:ml-auto sm:w-72">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter links" className="h-10 pl-9" />
        </div>
      </div>

      {isLoading ? (
        <Skeleton className="h-64 w-full rounded-xl" />
      ) : !list.length ? (
        <EmptyState
          icon={<LinkIcon />}
          title={data?.length ? "No links here" : "No shared links yet"}
          description={data?.length ? "Try another tab." : "Right-click a file or folder and choose Share."}
        />
      ) : (
        <div className="overflow-hidden rounded-xl border bg-card">
          {list.map((s) => (
            <ShareRow
              key={s.id}
              share={s}
              showOwner={all}
              onEdit={() => setEditing(s)}
              onRevoke={() => setRevoking(s)}
              onToggle={async () => {
                await api.updateShare(s.id, { disabled: !s.disabled }).catch((e) => toast.error(e.message))
                refresh()
              }}
            />
          ))}
        </div>
      )}

      <ShareDialog open={!!editing} share={editing ?? undefined} canUpload={editing?.isDir ?? false} onClose={() => setEditing(null)} />
      <AlertDialog open={!!revoking} onOpenChange={(o) => !o && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this link?</AlertDialogTitle>
            <AlertDialogDescription>
              Anyone using the link to “{revoking?.name}” loses access right away. The files are not touched. To stop it temporarily, pause it instead.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={revoke}>
              Delete link
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}

function ShareRow({ share: s, showOwner, onEdit, onRevoke, onToggle }: { share: Share; showOwner: boolean; onEdit: () => void; onRevoke: () => void; onToggle: () => void }) {
  const navigate = useNavigate()
  const { copied, copy } = useCopy()
  const st = status(s)
  return (
    <div className={cn("flex flex-col gap-3 border-b px-4 py-[var(--row-y)] last:border-b-0 md:flex-row md:items-center", st !== "active" && "opacity-70")}>
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <FileTile entry={s} />
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-medium">{s.title || s.name}</span>
            {st !== "active" && <Badge variant={st === "paused" ? "outline" : "destructive"}>{st === "missing" ? "Item missing" : st === "paused" ? "Paused" : "Expired"}</Badge>}
          </div>
          <div className="truncate text-xs text-muted-foreground">
            {s.note || (s.collection ? "Collection" : `${s.driveName} · ${parentPath(s.path) === "/" ? "" : parentPath(s.path)}`)}
            {showOwner && ` · ${s.owner}`}
          </div>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 md:w-64">
        {s.canView && <Badge variant="secondary">View</Badge>}
        {s.canDownload && <Badge variant="secondary">Download</Badge>}
        {s.canUpload && <Badge variant="secondary">Upload</Badge>}
        {s.canComment && <Badge variant="secondary">Review</Badge>}
        {s.access !== "link" && <Badge variant="outline">{s.access === "invited" ? "Invited only" : "Email confirmed"}</Badge>}
        {s.recipients.length > 0 && (
          <Badge variant="outline" title={s.recipients.map((r) => r.email).join(", ")}>
            {s.recipients.length} {s.recipients.length === 1 ? "person" : "people"}
          </Badge>
        )}
        {s.hasPassword && (
          <Badge variant="outline">
            <KeyRoundIcon /> Password
          </Badge>
        )}
        {s.allowedNets && <Badge variant="outline">Network limited</Badge>}
      </div>
      <div className="text-sm text-muted-foreground md:w-56">
        <div>{s.expiresAt ? `Expires ${formatDay(s.expiresAt)}` : "No expiry"}</div>
        <div className="text-xs">
          {s.downloads}
          {s.maxDownloads ? `/${s.maxDownloads}` : ""} downloads · {s.lastAccess ? `used ${relativeTime(s.lastAccess)}` : "not opened yet"}
        </div>
      </div>
      <div className="flex items-center gap-1 md:justify-end">
        <Button variant="outline" size="sm" onClick={() => copy(s.url)}>
          {copied ? <CheckIcon /> : <CopyIcon />} Copy link
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label="More" className="text-muted-foreground">
              <MoreVerticalIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            <DropdownMenuItem onSelect={onEdit}>
              <PencilIcon /> Edit options
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onToggle}>
              {s.disabled ? <PlayIcon /> : <PauseIcon />} {s.disabled ? "Resume" : "Pause"}
            </DropdownMenuItem>
            {s.collection ? (
              <DropdownMenuItem onSelect={() => navigate(`/collections/${s.collection}`)}>
                <FolderOpenIcon /> Open collection
              </DropdownMenuItem>
            ) : (
              s.place &&
              !s.missing && (
                <DropdownMenuItem onSelect={() => navigate(filesUrl(s.place!, s.isDir ? s.path : parentPath(s.path)))}>
                  <FolderOpenIcon /> Show in files
                </DropdownMenuItem>
              )
            )}
            <DropdownMenuItem asChild>
              <a href={s.url} target="_blank" rel="noopener noreferrer">
                <LinkIcon /> Open link
              </a>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onRevoke}>
              <Trash2Icon /> Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  )
}
