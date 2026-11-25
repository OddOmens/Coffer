import { useState, type ReactNode } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ActivityIcon,
  ArrowDownToLineIcon,
  ArrowUpFromLineIcon,
  CheckIcon,
  ClockIcon,
  CloudIcon,
  CopyIcon,
  CpuIcon,
  DownloadCloudIcon,
  HardDriveIcon,
  FilmIcon,
  LinkIcon,
  MailIcon,
  MemoryStickIcon,
  PlugIcon,
  ShieldAlertIcon,
  UploadIcon,
  UsersIcon,
} from "lucide-react"
import { Area, AreaChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"
import { Link } from "react-router"
import { toast } from "sonner"

import { AdminPage } from "@/components/admin-page"
import { HealthList } from "@/components/health-list"
import { Section } from "@/components/page"
import { Spinner } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api, type Sample } from "@/lib/api"
import { formatBytes, formatCount, formatDuration, relativeTime } from "@/lib/format"

const time = (t: number) => new Date(t * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })

function Stat({ icon, label, value, hint }: { icon: ReactNode; label: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="flex items-center justify-between gap-2 text-[0.8125rem] text-muted-foreground">
        <span className="truncate">{label}</span>
        <span className="grid size-6 shrink-0 place-items-center rounded-md bg-muted [&_svg]:size-3.5">{icon}</span>
      </div>
      <div className="mt-2 text-[1.75rem] leading-none font-medium tracking-tight tabular-nums">{value}</div>
      {hint && <div className="mt-2 text-xs text-muted-foreground">{hint}</div>}
    </div>
  )
}

function ChartCard({ title, children, empty }: { title: string; children: ReactNode; empty: boolean }) {
  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="mb-3 text-sm font-semibold">{title}</div>
      {empty ? (
        <div className="flex h-48 items-center justify-center text-sm text-muted-foreground">Collecting data… (one point per minute)</div>
      ) : (
        children
      )}
    </div>
  )
}

const reqConfig = {
  requests: { label: "Requests", color: "var(--chart-1)" },
  errors: { label: "Errors", color: "var(--destructive)" },
} satisfies ChartConfig
const bwConfig = {
  bytesOut: { label: "Sent", color: "var(--chart-4)" },
  bytesIn: { label: "Received", color: "var(--chart-2)" },
} satisfies ChartConfig
const cpuConfig = { cpu: { label: "CPU %", color: "var(--chart-3)" } } satisfies ChartConfig
const memConfig = { mem: { label: "Memory", color: "var(--chart-5)" } } satisfies ChartConfig

function CopyCommand({ cmd }: { cmd: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex items-center gap-2 rounded-lg border bg-muted/40 p-2">
      <code className="flex-1 truncate px-1 font-mono text-xs">{cmd}</code>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="Copy command"
        onClick={() =>
          navigator.clipboard.writeText(cmd).then(
            () => {
              setCopied(true)
              setTimeout(() => setCopied(false), 1500)
            },
            () => toast.error("Could not copy"),
          )
        }
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </Button>
    </div>
  )
}

export default function MonitorPage() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ["monitor"], queryFn: api.monitor, refetchInterval: 10_000 })
  const [checking, setChecking] = useState(false)

  if (isLoading || !data) {
    return (
      <AdminPage>
        <Skeleton className="h-96 w-full rounded-xl" />
      </AdminPage>
    )
  }

  const sys = data.system
  const hist: Sample[] = sys.history ?? []
  const last = hist[hist.length - 1]
  const empty = hist.length < 2
  const st = data.stats
  const up = data.update

  const check = async () => {
    setChecking(true)
    try {
      await api.checkUpdates()
      await qc.invalidateQueries({ queryKey: ["monitor"] })
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setChecking(false)
    }
  }

  return (
    <AdminPage description="Live server health. Refreshes every 10 seconds; history covers the last 24 hours.">
      <HealthList problems={data.health} className="mb-6" />
      <div className="mb-8 grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
        <Stat icon={<ClockIcon />} label="Uptime" value={formatDuration(sys.uptime)} hint={`version ${data.version}`} />
        <Stat icon={<CpuIcon />} label="CPU" value={last ? `${last.cpu.toFixed(1)}%` : "—"} hint={`of one core · ${sys.cpus} cores`} />
        <Stat icon={<MemoryStickIcon />} label="Memory" value={formatBytes(sys.mem)} hint={`${formatBytes(sys.heap)} in use`} />
        <Stat icon={<PlugIcon />} label="Connections" value={formatCount(sys.conns)} hint={`${sys.goroutines} goroutines`} />
        <Stat icon={<UsersIcon />} label="Active sessions" value={formatCount(st.sessions ?? 0)} hint={`${st.users ?? 0} users`} />
        <Stat icon={<LinkIcon />} label="Active links" value={formatCount(st.activeShares ?? 0)} />
        <Stat icon={<UploadIcon />} label="Uploads in progress" value={formatCount(st.uploadsInProgress ?? 0)} />
        <Stat icon={<ArrowDownToLineIcon />} label="Link downloads" value={formatCount(st.shareDownloads24h ?? 0)} hint="last 24 h" />
        <Stat icon={<ArrowUpFromLineIcon />} label="Link uploads" value={formatCount(st.shareUploads24h ?? 0)} hint="last 24 h" />
        <Stat icon={<ShieldAlertIcon />} label="Failed sign-ins" value={formatCount(st.failedLogins24h ?? 0)} hint="last 24 h" />
      </div>

      <Section title="Activity">
        <div className="grid gap-4 lg:grid-cols-2">
          <ChartCard title="Requests per minute" empty={empty}>
            <ChartContainer config={reqConfig} className="h-48 w-full">
              <AreaChart data={hist} margin={{ left: 0, right: 8 }}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="t" tickFormatter={time} tickLine={false} axisLine={false} minTickGap={40} />
                <YAxis width={36} tickLine={false} axisLine={false} allowDecimals={false} />
                <ChartTooltip content={<ChartTooltipContent labelFormatter={(_, p) => time(p?.[0]?.payload?.t ?? 0)} />} />
                <Area dataKey="requests" type="monotone" stroke="var(--color-requests)" fill="var(--color-requests)" fillOpacity={0.15} />
                <Area dataKey="errors" type="monotone" stroke="var(--color-errors)" fill="var(--color-errors)" fillOpacity={0.2} />
              </AreaChart>
            </ChartContainer>
          </ChartCard>
          <ChartCard title="Bandwidth per minute" empty={empty}>
            <ChartContainer config={bwConfig} className="h-48 w-full">
              <AreaChart data={hist} margin={{ left: 0, right: 8 }}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="t" tickFormatter={time} tickLine={false} axisLine={false} minTickGap={40} />
                <YAxis width={64} tickLine={false} axisLine={false} tickFormatter={(v) => formatBytes(v)} />
                <ChartTooltip
                  content={
                    <ChartTooltipContent
                      labelFormatter={(_, p) => time(p?.[0]?.payload?.t ?? 0)}
                      formatter={(v, n) => (
                        <span className="flex w-full justify-between gap-3">
                          <span className="text-muted-foreground">{bwConfig[n as keyof typeof bwConfig]?.label}</span>
                          <span className="font-mono">{formatBytes(Number(v))}</span>
                        </span>
                      )}
                    />
                  }
                />
                <Area dataKey="bytesOut" type="monotone" stroke="var(--color-bytesOut)" fill="var(--color-bytesOut)" fillOpacity={0.15} />
                <Area dataKey="bytesIn" type="monotone" stroke="var(--color-bytesIn)" fill="var(--color-bytesIn)" fillOpacity={0.15} />
              </AreaChart>
            </ChartContainer>
          </ChartCard>
          <ChartCard title="CPU" empty={empty}>
            <ChartContainer config={cpuConfig} className="h-48 w-full">
              <LineChart data={hist} margin={{ left: 0, right: 8 }}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="t" tickFormatter={time} tickLine={false} axisLine={false} minTickGap={40} />
                <YAxis width={36} tickLine={false} axisLine={false} unit="%" />
                <ChartTooltip content={<ChartTooltipContent labelFormatter={(_, p) => time(p?.[0]?.payload?.t ?? 0)} />} />
                <Line dataKey="cpu" type="monotone" stroke="var(--color-cpu)" dot={false} strokeWidth={2} />
              </LineChart>
            </ChartContainer>
          </ChartCard>
          <ChartCard title="Memory" empty={empty}>
            <ChartContainer config={memConfig} className="h-48 w-full">
              <LineChart data={hist} margin={{ left: 0, right: 8 }}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="t" tickFormatter={time} tickLine={false} axisLine={false} minTickGap={40} />
                <YAxis width={64} tickLine={false} axisLine={false} tickFormatter={(v) => formatBytes(v)} />
                <ChartTooltip content={<ChartTooltipContent labelFormatter={(_, p) => time(p?.[0]?.payload?.t ?? 0)} />} />
                <Line dataKey="mem" type="monotone" stroke="var(--color-mem)" dot={false} strokeWidth={2} />
              </LineChart>
            </ChartContainer>
          </ChartCard>
        </div>
      </Section>

      <Section title="Drives">
        <div className="overflow-hidden rounded-xl border bg-card">
          <Table>
            <TableHeader className="bg-muted/40">
              <TableRow>
                <TableHead className="pl-4">Drive</TableHead>
                <TableHead>Usage</TableHead>
                <TableHead className="hidden md:table-cell">Indexed</TableHead>
                <TableHead className="hidden lg:table-cell">Last scan</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.drives.map((d) => {
                const used = d.total ? d.total - d.free : 0
                return (
                  <TableRow key={d.id}>
                    <TableCell className="pl-4">
                      <div className="flex items-center gap-2.5">
                        {d.kind === "s3" ? <CloudIcon className="size-4 text-muted-foreground" /> : <HardDriveIcon className="size-4 text-muted-foreground" />}
                        <span className="font-medium">{d.name}</span>
                        {!d.online && <Badge variant="destructive">Offline</Badge>}
                      </div>
                    </TableCell>
                    <TableCell className="min-w-40">
                      {d.total ? (
                        <div className="space-y-1">
                          <div className="h-1.5 overflow-hidden rounded-full bg-secondary">
                            <div className="h-full rounded-full bg-brand" style={{ width: `${(used / d.total) * 100}%` }} />
                          </div>
                          <div className="text-xs text-muted-foreground">
                            {formatBytes(d.free)} free of {formatBytes(d.total)}
                          </div>
                        </div>
                      ) : (
                        <span className="text-xs text-muted-foreground">{formatBytes(d.scan.bytes)} stored</span>
                      )}
                    </TableCell>
                    <TableCell className="hidden text-sm text-muted-foreground md:table-cell">
                      {formatCount(d.scan.files)} files · {formatCount(d.scan.dirs)} folders
                    </TableCell>
                    <TableCell className="hidden text-sm text-muted-foreground lg:table-cell">
                      {d.scan.running ? (
                        <span className="flex items-center gap-1.5">
                          <Spinner className="size-3.5" /> Scanning
                        </span>
                      ) : d.scan.error ? (
                        <span className="block max-w-sm truncate text-destructive" title={d.scan.error}>Failed: {d.scan.error}</span>
                      ) : d.scan.finished ? (
                        `${relativeTime(d.scan.finished)} · ${(d.scan.ms / 1000).toFixed(1)} s`
                      ) : (
                        "Never"
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      </Section>

      <Section title="Services">
        <div className="grid gap-4 lg:grid-cols-2">
          <div className="rounded-xl border bg-card p-4">
            <div className="mb-3 flex items-center justify-between gap-2">
              <div className="flex items-center gap-2 font-semibold">
                <FilmIcon className="size-4" /> Media worker
              </div>
              <Badge variant={data.media?.workerOnline ? "default" : "secondary"} className={data.media?.workerOnline ? "bg-emerald-600 text-white" : undefined}>
                {data.media?.workerOnline ? "Online" : "Offline"}
              </Badge>
            </div>
            <dl className="grid grid-cols-4 gap-3 text-sm">
              {(["queued", "running", "done", "failed"] as const).map((k) => (
                <div key={k}>
                  <dt className="text-muted-foreground capitalize">{k}</dt>
                  <dd className={`font-medium tabular-nums ${k === "failed" && (data.media?.jobs[k] ?? 0) > 0 ? "text-destructive" : ""}`}>
                    {formatCount(data.media?.jobs[k] ?? 0)}
                  </dd>
                </div>
              ))}
            </dl>
            <p className="mt-4 text-xs text-muted-foreground">
              {data.media?.workerOnline
                ? "Makes video previews, video thumbnails and HEIC/RAW photo previews."
                : "Video previews and HEIC/RAW photos need the media service. Start it with:"}
            </p>
            {!data.media?.workerOnline && (
              <div className="mt-2">
                <CopyCommand cmd="docker compose up -d media" />
              </div>
            )}
          </div>
          <div className="rounded-xl border bg-card p-4">
            <div className="mb-3 flex items-center justify-between gap-2">
              <div className="flex items-center gap-2 font-semibold">
                <MailIcon className="size-4" /> Email
              </div>
              <Badge variant={data.mail ? "default" : "secondary"} className={data.mail ? "bg-emerald-600 text-white" : undefined}>
                {data.mail ? "Set up" : "Not set up"}
              </Badge>
            </div>
            <p className="text-sm text-muted-foreground">
              {data.mail
                ? "Invitations, sign-in codes for links and notifications are sent by email."
                : "Without email, links can't ask visitors to confirm their address and invitations can't be sent."}
            </p>
            <Button variant="outline" size="sm" className="mt-3" asChild>
              <Link to="/admin/settings">{data.mail ? "Email settings" : "Set up email"}</Link>
            </Button>
          </div>
        </div>
      </Section>

      <Section title="Software">
        <div className="grid gap-4 lg:grid-cols-2">
          <div className="rounded-xl border bg-card p-4">
            <dl className="grid grid-cols-2 gap-4 text-sm">
              <div>
                <dt className="text-muted-foreground">Coffer</dt>
                <dd className="font-medium">{data.version}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Go</dt>
                <dd className="font-medium">{sys.goVersion}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">CPU cores</dt>
                <dd className="font-medium">{sys.cpus}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Load average</dt>
                <dd className="font-medium tabular-nums">{sys.load.map((l) => l.toFixed(2)).join(" · ")}</dd>
              </div>
            </dl>
            <p className="mt-5 text-xs text-muted-foreground">
              Prometheus metrics are available at <code className="font-mono">/metrics</code> when{" "}
              <code className="font-mono">COFFER_METRICS_TOKEN</code> is set (send it as a bearer token).
            </p>
          </div>
          <div className="rounded-xl border bg-card p-4">
            <div className="mb-3 flex items-center justify-between gap-2">
              <div className="flex items-center gap-2 font-semibold">
                <DownloadCloudIcon className="size-4" /> Updates
              </div>
              <Button variant="outline" size="sm" onClick={check} disabled={checking || !up.enabled}>
                {checking ? <Spinner /> : <ActivityIcon />} Check now
              </Button>
            </div>
            {!up.enabled ? (
              <p className="text-sm text-muted-foreground">Update checks are turned off in App settings or by COFFER_UPDATE_CHECK.</p>
            ) : up.available && up.latest ? (
              <div className="space-y-3 text-sm">
                <p>
                  <Badge className="mr-2">New</Badge>
                  Coffer <span className="font-semibold">{up.latest.version}</span> is available (you have {up.current}).
                </p>
                {up.latest.url && (
                  <a href={up.latest.url} target="_blank" rel="noopener noreferrer" className="text-brand underline-offset-4 hover:underline">
                    Release notes
                  </a>
                )}
                <p className="text-muted-foreground">Update from the folder with docker-compose.yml:</p>
                <CopyCommand cmd="docker compose pull && docker compose up -d" />
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">
                {up.error
                  ? `Could not check: ${up.error}`
                  : up.checkedAt
                    ? `You are up to date (${up.current}). Checked ${relativeTime(up.checkedAt)}.`
                    : "Not checked yet."}
              </p>
            )}
          </div>
        </div>
      </Section>
    </AdminPage>
  )
}
