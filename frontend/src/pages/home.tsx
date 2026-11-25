import { useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { CloudIcon, FolderOpenIcon, HardDriveIcon, LinkIcon, SendIcon, UsersRoundIcon } from "lucide-react"
import { Link, useNavigate } from "react-router"

import { usePlaces, useUser } from "@/components/app-layout"
import { hitUrl } from "@/components/command-search"
import { HealthList, useHealth } from "@/components/health-list"
import { FileTile } from "@/components/file-icon"
import { FileCard } from "@/components/file-list"
import { Page, Section } from "@/components/page"
import { SendDialog } from "@/components/send-dialog"
import { EmptyState } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api, type Kind, type Place } from "@/lib/api"
import { fileCount, filesUrl, formatBytes, parentPath, relativeTime } from "@/lib/format"

const kindColors: Partial<Record<Kind, string>> = {
  video: "bg-violet-500",
  image: "bg-emerald-500",
  design: "bg-indigo-500",
  audio: "bg-pink-500",
  document: "bg-blue-500",
  pdf: "bg-red-500",
  spreadsheet: "bg-green-500",
  presentation: "bg-orange-500",
  archive: "bg-amber-500",
}
const kindLabel = (k: string) => (k === "design" ? "Projects" : k[0].toUpperCase() + k.slice(1))

function greeting() {
  const h = new Date().getHours()
  return h < 5 ? "Good evening" : h < 12 ? "Good morning" : h < 18 ? "Good afternoon" : "Good evening"
}

/** A figure in the row at the top: small label, big number, a line under it. */
function Stat({ label, icon: Icon, value, detail, to }: { label: string; icon: typeof SendIcon; value: ReactNode; detail: ReactNode; to: string }) {
  return (
    <Link to={to} className="group min-w-0 rounded-xl border bg-card p-3 transition-colors hover:border-ring/50 sm:p-4">
      <div className="flex items-center justify-between gap-2 text-[0.8125rem] text-muted-foreground">
        <span className="truncate">{label}</span>
        <span className="grid size-6 shrink-0 place-items-center max-sm:hidden rounded-md bg-muted text-muted-foreground transition-colors group-hover:text-foreground">
          <Icon className="size-3.5" />
        </span>
      </div>
      <div className="mt-2 text-2xl leading-none font-medium tracking-tight tabular-nums sm:text-[1.75rem]">{value}</div>
      <div className="mt-2.5 truncate text-xs text-muted-foreground max-sm:hidden">{detail}</div>
    </Link>
  )
}

function StoragePanel({ place }: { place: Place }) {
  const { data } = useQuery({ queryKey: ["breakdown", place.id], queryFn: () => api.breakdown(place.id), staleTime: 60_000 })
  const total = (data ?? []).reduce((n, k) => n + k.bytes, 0)
  const top = (data ?? []).slice(0, 6)
  const rest = total - top.reduce((n, k) => n + k.bytes, 0)
  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <div className="text-[0.8125rem] text-muted-foreground">{place.name}</div>
          <div className="mt-2 text-[1.75rem] leading-none font-medium tracking-tight tabular-nums">{formatBytes(place.size)}</div>
          <div className="mt-2 text-xs text-muted-foreground">
            {fileCount(place.items)}
            {place.total > 0 && ` · ${formatBytes(place.free)} free of ${formatBytes(place.total)}`}
          </div>
        </div>
        {place.driveKind === "s3" ? <CloudIcon className="size-4 text-muted-foreground" /> : <HardDriveIcon className="size-4 text-muted-foreground" />}
      </div>
      {total > 0 ? (
        <>
          <div className="mb-3 flex h-2 overflow-hidden rounded-full bg-secondary">
            {top.map((k) => (
              <div key={k.kind} className={kindColors[k.kind] ?? "bg-slate-400"} style={{ width: `${(k.bytes / total) * 100}%` }} title={kindLabel(k.kind)} />
            ))}
            {rest > 0 && <div className="bg-slate-300 dark:bg-slate-600" style={{ width: `${(rest / total) * 100}%` }} />}
          </div>
          <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-[0.8125rem]">
            {top.map((k) => (
              <div key={k.kind} className="flex items-center gap-2">
                <span className={`size-2 rounded-full ${kindColors[k.kind] ?? "bg-slate-400"}`} />
                <span className="truncate">{kindLabel(k.kind)}</span>
                <span className="ml-auto text-muted-foreground tabular-nums">{formatBytes(k.bytes)}</span>
              </div>
            ))}
          </div>
        </>
      ) : (
        <p className="text-sm text-muted-foreground">Nothing indexed yet. Sizes appear after the first scan.</p>
      )}
    </div>
  )
}

export default function HomePage() {
  const me = useUser()
  const navigate = useNavigate()
  const places = usePlaces()
  const recent = useQuery({ queryKey: ["recent", 12], queryFn: () => api.recent(12) })
  const shares = useQuery({ queryKey: ["shares"], queryFn: () => api.shares() })
  const homeId = places.data?.home
  const home = places.data?.places.find((p) => p.id === homeId)
  const shared = places.data?.places.filter((p) => p.kind === "shared") ?? []
  const activeLinks = (shares.data ?? []).filter((s) => s.active)
  const [sending, setSending] = useState(false)
  const health = useHealth(me.role === "admin")

  return (
    <Page
      title={`${greeting()}, ${me.username}`}
      description="Here's what's been happening with your files."
      actions={
        <>
          <Button variant="outline" className="rounded-full" aria-label="Send files" onClick={() => setSending(true)}>
            <SendIcon /> <span className="max-sm:hidden">Send files</span>
          </Button>
          <Button className="rounded-full max-sm:hidden" asChild>
            <Link to={homeId ? filesUrl(homeId) : "/files"} aria-label="Browse files">
              <FolderOpenIcon /> <span className="max-sm:hidden">Browse files</span>
            </Link>
          </Button>
        </>
      }
    >
      <HealthList problems={health.data?.problems} className="mb-6" />
      <div className="mb-6 grid grid-cols-3 gap-2 sm:gap-3">
        <Stat
          label="Files"
          icon={FolderOpenIcon}
          to={homeId ? filesUrl(homeId) : "/files"}
          value={home ? home.items.toLocaleString() : "–"}
          detail={home ? `${formatBytes(home.size)} in ${home.name}` : "\u00a0"}
        />
        <Stat label="Shared with me" icon={UsersRoundIcon} to="/shared" value={shared.length} detail={shared.length === 1 ? "Folder from someone else" : "Folders from other people"} />
        <Stat label="Active links" icon={LinkIcon} to="/shares" value={activeLinks.length} detail={`${shares.data?.length ?? 0} links in total`} />
      </div>

      <Section
        title="Recently changed"
        actions={
          homeId && (
            <Button variant="outline" size="sm" asChild>
              <Link to={filesUrl(homeId)}>View all files</Link>
            </Button>
          )
        }
      >
        {recent.isLoading ? (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-[62px] rounded-xl" />
            ))}
          </div>
        ) : recent.data?.entries.length ? (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
            {recent.data.entries.map((h) => (
              <FileCard
                key={h.place + h.path}
                entry={h}
                onOpen={() => navigate(hitUrl(h))}
                subtitle={`${relativeTime(h.modTime)} · ${h.placeName}${parentPath(h.path) !== "/" ? " · " + parentPath(h.path).split("/").pop() : ""}`}
                actions={[
                  { label: "Open", icon: <FolderOpenIcon />, onSelect: () => navigate(hitUrl(h)) },
                  { label: "Show in folder", icon: <FolderOpenIcon />, onSelect: () => navigate(filesUrl(h.place, parentPath(h.path))) },
                ]}
              />
            ))}
          </div>
        ) : (
          <EmptyState icon={<FolderOpenIcon />} title="No files yet" description="Upload something to get started." />
        )}
      </Section>

      <div className="grid gap-x-6 lg:grid-cols-[1fr_20rem]">
        <Section title="Storage">
          <div className="grid gap-3 md:grid-cols-2">
            {home && <StoragePanel place={home} />}
            {places.data?.places
              .filter((p) => p.id !== homeId && p.kind !== "shared")
              .slice(0, 3)
              .map((p) => <StoragePanel key={p.id} place={p} />)}
          </div>
        </Section>
        <Section title="Shared with me" actions={<Link to="/shared" className="text-sm font-medium text-muted-foreground hover:text-foreground">See all</Link>}>
          <div className="rounded-xl border bg-card">
            {shared.length === 0 ? (
              <p className="p-4 text-sm text-muted-foreground">When someone shares a folder with you, it shows up here.</p>
            ) : (
              shared.slice(0, 6).map((p) => (
                <Link key={p.id} to={filesUrl(p.id)} className="flex items-center gap-3 border-b px-3 py-2 last:border-b-0 hover:bg-muted/50">
                  <FileTile entry={{ name: p.name, isDir: true }} size="sm" />
                  <div className="min-w-0">
                    <div className="truncate text-sm font-medium">{p.name}</div>
                    <div className="truncate text-xs text-muted-foreground">from {p.grantedBy}</div>
                  </div>
                </Link>
              ))
            )}
          </div>
        </Section>
      </div>
      <SendDialog open={sending} onClose={() => setSending(false)} />
    </Page>
  )
}
