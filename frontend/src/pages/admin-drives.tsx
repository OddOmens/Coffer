import { useEffect, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  AlertCircleIcon,
  CalculatorIcon,
  CheckCircle2Icon,
  CheckIcon,
  CloudIcon,
  CopyIcon,
  DownloadIcon,
  HardDriveIcon,
  KeyRoundIcon,
  LockIcon,
  MoreHorizontalIcon,
  PauseIcon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  RefreshCwIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { AdminPage } from "@/components/admin-page"
import { B2CostDialog } from "@/components/b2-cost-dialog"
import { EmptyState, Spinner } from "@/components/states"
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
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api, type Drive, type S3Config } from "@/lib/api"
import { b2Cost, formatMoney, isB2 as isB2Endpoint, toGB, useB2Rate } from "@/lib/b2"
import { formatBytes, formatCount, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

export default function DrivesPage() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ["drives"],
    queryFn: api.drives,
    refetchInterval: (q) => (q.state.data?.drives.some((d) => d.scan.running) ? 3000 : 30_000),
  })
  const [editing, setEditing] = useState<Drive | "new" | null>(null)
  const [deleting, setDeleting] = useState<Drive | null>(null)
  // A drive key to save: just made (must be confirmed) or asked for again.
  const [keyShown, setKeyShown] = useState<{ name: string; key: string; fresh: boolean } | null>(null)
  const [askKey, setAskKey] = useState<Drive | null>(null)
  const [costs, setCosts] = useState(false)

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["drives"] })
    qc.invalidateQueries({ queryKey: ["places"] })
  }

  const act = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn()
      toast.success(ok)
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  return (
    <AdminPage
      description="Storage that Coffer serves: folders on this server and S3-compatible buckets such as Cloudflare R2."
      actions={
        <>
          <Button variant="outline" onClick={() => setCosts(true)}>
            <CalculatorIcon /> Backblaze costs
          </Button>
          <Button onClick={() => setEditing("new")}>
            <PlusIcon /> Add drive
          </Button>
        </>
      }
    >
      {isLoading ? (
        <Skeleton className="h-48 w-full rounded-xl" />
      ) : !data?.drives.length ? (
        <EmptyState icon={<HardDriveIcon />} title="No drives" />
      ) : (
        <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
          {data.drives.map((d) => (
            <DriveCard
              key={d.id}
              d={d}
              onEdit={() => setEditing(d)}
              onDelete={() => setDeleting(d)}
              onRescan={() => act(() => api.rescanDrive(d.id), "Rescan started")}
              onShowKey={() => setAskKey(d)}
              onToggle={() => act(() => api.updateDrive(d.id, { disabled: !d.disabled }), d.disabled ? "Drive enabled" : "Drive disabled")}
            />
          ))}
        </div>
      )}
      <DriveDialog
        drive={editing}
        roots={data?.roots ?? []}
        onClose={() => setEditing(null)}
        onSaved={(d) => {
          refresh()
          if (d.recoveryKey) setKeyShown({ name: d.name, key: d.recoveryKey, fresh: true })
        }}
      />
      <ShowKeyDialog drive={askKey} onClose={() => setAskKey(null)} onShown={(d, key) => setKeyShown({ name: d.name, key, fresh: false })} />
      <RecoveryKeyDialog shown={keyShown} onClose={() => setKeyShown(null)} />
      <B2CostDialog open={costs} onClose={() => setCosts(false)} />
      <AlertDialog open={!!deleting} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove “{deleting?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>
              Coffer forgets this drive, along with its user folders, share links and trash records. The files on the drive are not
              touched.
              {deleting?.s3?.encrypt && (
                <span className="mt-2 block font-medium text-destructive">
                  The files in this bucket are encrypted. Make sure you have saved the drive's recovery key first: without it they can never be
                  read again.
                </span>
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => deleting && act(() => api.deleteDrive(deleting.id), "Drive removed").then(() => setDeleting(null))}
            >
              Remove drive
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </AdminPage>
  )
}

function DriveCard({
  d,
  onEdit,
  onDelete,
  onRescan,
  onToggle,
  onShowKey,
}: {
  d: Drive
  onEdit: () => void
  onDelete: () => void
  onRescan: () => void
  onToggle: () => void
  onShowKey: () => void
}) {
  const Icon = d.kind === "s3" ? CloudIcon : HardDriveIcon
  const used = d.total ? d.total - d.free : 0
  const rate = useB2Rate()
  const where = d.kind === "s3" ? `${d.s3?.bucket ?? ""}${d.s3?.prefix ? "/" + d.s3.prefix : ""} · ${d.s3?.endpoint ?? ""}` : d.path
  return (
    <div className={cn("rounded-xl border bg-card p-4", d.disabled && "opacity-70")}>
      <div className="flex items-start gap-3">
        <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-tile text-tile-foreground">
          <Icon className="size-5 stroke-[1.75]" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="truncate font-semibold">{d.name}</span>
            <Badge variant="secondary">{d.kind === "s3" ? "S3 / R2" : "Local"}</Badge>
            {d.s3?.encrypt && (
              <Badge variant="outline" title="Files are encrypted before they reach the bucket">
                <LockIcon /> Encrypted
              </Badge>
            )}
            {d.disabled ? (
              <Badge variant="outline">Disabled</Badge>
            ) : d.online ? (
              <Badge className="bg-emerald-600 text-white">Online</Badge>
            ) : (
              <Badge variant="destructive">Offline</Badge>
            )}
          </div>
          <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground" title={where}>
            {where}
          </div>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${d.name}`}>
              <MoreHorizontalIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={onRescan} disabled={!d.online || d.scan.running}>
              <RefreshCwIcon /> Rescan now
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onEdit}>
              <PencilIcon /> Edit
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onToggle}>
              {d.disabled ? <PlayIcon /> : <PauseIcon />} {d.disabled ? "Enable" : "Disable"}
            </DropdownMenuItem>
            {d.s3?.encrypt && (
              <DropdownMenuItem onSelect={onShowKey}>
                <KeyRoundIcon /> Show recovery key
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onDelete}>
              <Trash2Icon /> Remove
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {d.error && !d.online && !d.disabled && (
        <div className="mt-3 flex gap-2 rounded-lg border border-destructive/30 bg-destructive/5 p-2.5 text-sm text-destructive">
          <AlertCircleIcon className="mt-0.5 size-4 shrink-0" /> {d.error}
        </div>
      )}
      {d.total > 0 && (
        <div className="mt-4">
          <div className="mb-1.5 flex justify-between text-xs text-muted-foreground">
            <span>{formatBytes(used)} used</span>
            <span>{formatBytes(d.free)} free of {formatBytes(d.total)}</span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-secondary">
            <div className="h-full rounded-full bg-brand" style={{ width: `${Math.min(100, (used / d.total) * 100)}%` }} />
          </div>
        </div>
      )}
      <div className="mt-4 grid grid-cols-3 gap-3 border-t pt-4 text-sm">
        <div>
          <div className="text-xs text-muted-foreground">Indexed</div>
          <div className="font-medium tabular-nums">{formatBytes(d.scan.bytes)}</div>
        </div>
        <div>
          <div className="text-xs text-muted-foreground">Files</div>
          <div className="font-medium tabular-nums">{formatCount(d.scan.files)}</div>
        </div>
        <div>
          <div className="text-xs text-muted-foreground">Last scan</div>
          <div className="flex items-center gap-1.5 font-medium">
            {d.scan.running ? (
              <>
                <Spinner className="size-3.5" /> Scanning…
              </>
            ) : d.scan.finished ? (
              <span title={`${(d.scan.ms / 1000).toFixed(1)} s`}>{relativeTime(d.scan.finished)}</span>
            ) : (
              "Never"
            )}
          </div>
        </div>
      </div>
      {d.scan.error && <p className="mt-2 text-xs text-destructive">Last scan failed: {d.scan.error}</p>}
      {d.kind === "s3" && isB2Endpoint(d.s3?.endpoint) && (
        <p className="mt-2 text-xs text-muted-foreground tabular-nums">
          About {formatMoney(b2Cost(toGB(d.scan.bytes), rate).month)} a month to store in Backblaze
        </p>
      )}
    </div>
  )
}

const emptyS3: S3Config = {
  endpoint: "",
  region: "auto",
  bucket: "",
  prefix: "",
  accessKey: "",
  secretKey: "",
  insecure: false,
  pathStyle: false,
  directDownloads: true,
  encrypt: false,
}

const presets: { id: string; label: string; apply: (c: S3Config) => S3Config }[] = [
  { id: "r2", label: "Cloudflare R2", apply: (c) => ({ ...c, endpoint: "<ACCOUNT_ID>.r2.cloudflarestorage.com", region: "auto", pathStyle: false, insecure: false }) },
  { id: "aws", label: "AWS S3", apply: (c) => ({ ...c, endpoint: "s3.us-east-1.amazonaws.com", region: "us-east-1", pathStyle: false, insecure: false }) },
  { id: "b2", label: "Backblaze B2", apply: (c) => ({ ...c, endpoint: "s3.<REGION>.backblazeb2.com", region: "", pathStyle: false, insecure: false }) },
  { id: "other", label: "MinIO / other", apply: (c) => ({ ...c, endpoint: "minio.local:9000", region: "us-east-1", pathStyle: true }) },
]

function DriveDialog({
  drive,
  roots,
  onClose,
  onSaved,
}: {
  drive: Drive | "new" | null
  roots: string[]
  onClose: () => void
  onSaved: (d: Drive) => void
}) {
  const existing = drive && drive !== "new" ? drive : null
  const [kind, setKind] = useState<"local" | "s3">("local")
  const [name, setName] = useState("")
  const [path, setPath] = useState("")
  const [s3, setS3] = useState<S3Config>(emptyS3)
  const [recoveryKey, setRecoveryKey] = useState("")
  const [busy, setBusy] = useState(false)
  const [test, setTest] = useState<{ ok: boolean; error?: string } | null>(null)
  const [testing, setTesting] = useState(false)

  useEffect(() => {
    if (!drive) return
    setTest(null)
    setRecoveryKey("")
    if (existing) {
      setKind(existing.kind)
      setName(existing.name)
      setPath(existing.path ?? "")
      setS3({ ...emptyS3, ...(existing.s3 ?? {}), secretKey: "" })
    } else {
      setKind("local")
      setName("")
      setPath(roots[0] ? `${roots[0]}/` : "/srv/")
      setS3(presets[0].apply(emptyS3))
    }
  }, [drive]) // eslint-disable-line react-hooks/exhaustive-deps

  const body = () => {
    const b: Record<string, unknown> = { name, kind }
    if (kind === "local") {
      if (!existing || path !== existing.path) b.path = path
    } else {
      // Re-sending the bucket settings re-validates them and rebuilds the index,
      // so only send them when something changed.
      const same =
        existing && !s3.secretKey && JSON.stringify({ ...s3, secretKey: "" }) === JSON.stringify({ ...emptyS3, ...(existing.s3 ?? {}), secretKey: "" })
      if (!same || recoveryKey.trim()) b.s3 = s3
      if (s3.encrypt && recoveryKey.trim()) b.recoveryKey = recoveryKey
    }
    return b
  }

  const runTest = async () => {
    setTesting(true)
    setTest(null)
    try {
      setTest(
        await api.testDrive(
          { name, kind, path, s3: kind === "s3" ? s3 : undefined, recoveryKey: kind === "s3" && s3.encrypt ? recoveryKey : undefined },
          existing?.id,
        ),
      )
    } catch (e) {
      setTest({ ok: false, error: (e as Error).message })
    } finally {
      setTesting(false)
    }
  }

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    setBusy(true)
    try {
      const saved = existing ? await api.updateDrive(existing.id, body()) : await api.createDrive(body())
      toast.success(existing ? "Drive updated" : "Drive added; indexing has started")
      onSaved(saved)
      onClose()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const set = <K extends keyof S3Config>(k: K, v: S3Config[K]) => setS3({ ...s3, [k]: v })
  const isB2 = /backblazeb2\.com/i.test(s3.endpoint)
  // Endpoints like s3.us-west-004.backblazeb2.com name their region; requests must be signed for it.
  const setEndpoint = (endpoint: string) => {
    const m = endpoint.trim().replace(/^https?:\/\//i, "").replace(/\/+$/, "").toLowerCase().match(/^s3[.-]([a-z0-9-]+)\.(backblazeb2|amazonaws)\.com(:\d+)?$/)
    setS3({ ...s3, endpoint, region: m ? m[1] : s3.region })
  }

  return (
    <Dialog open={!!drive} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-xl">
        <form onSubmit={submit} className="grid gap-5">
          <DialogHeader>
            <DialogTitle>{existing ? `Edit ${existing.name}` : "Add drive"}</DialogTitle>
            <DialogDescription>
              {kind === "local"
                ? "A folder on this server. It is also available over SMB if it is mounted into the Samba container."
                : "S3-compatible buckets work in the web app, share links and WebDAV, but not over SMB."}
            </DialogDescription>
          </DialogHeader>
          {!existing && (
            <Tabs value={kind} onValueChange={(v) => { setKind(v as "local" | "s3"); setTest(null) }}>
              <TabsList className="w-full">
                <TabsTrigger value="local">
                  <HardDriveIcon /> Local folder
                </TabsTrigger>
                <TabsTrigger value="s3">
                  <CloudIcon /> S3 / Cloudflare R2
                </TabsTrigger>
              </TabsList>
            </Tabs>
          )}
          <FieldGroup className="gap-4">
            <Field>
              <FieldLabel htmlFor="d-name">Name</FieldLabel>
              <Input id="d-name" required maxLength={48} placeholder={kind === "s3" ? "Archive (R2)" : "Projects"} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            {kind === "local" ? (
              <Field>
                <FieldLabel htmlFor="d-path">Folder path inside the container</FieldLabel>
                <Input id="d-path" required className="font-mono" value={path} onChange={(e) => setPath(e.target.value)} />
                <FieldDescription>
                  Must be inside {roots.length ? roots.map((r) => <code key={r} className="mx-0.5 font-mono">{r}</code>) : "the allowed roots"}. Mount
                  the host folder there in docker-compose.yml first, for example <code className="font-mono">- /mnt/raid/projects:/srv/projects</code>, then
                  run <code className="font-mono">docker compose up -d</code>.
                </FieldDescription>
              </Field>
            ) : (
              <>
                {!existing && (
                  <div className="flex flex-wrap gap-2">
                    {presets.map((p) => (
                      <Button key={p.id} type="button" variant="outline" size="sm" onClick={() => setS3(p.apply(s3))}>
                        {p.label}
                      </Button>
                    ))}
                  </div>
                )}
                <Field>
                  <FieldLabel htmlFor="s3-ep">Endpoint</FieldLabel>
                  <Input id="s3-ep" required className="font-mono" value={s3.endpoint} onChange={(e) => setEndpoint(e.target.value)} />
                  <FieldDescription>
                    {isB2 ? (
                      <>
                        Copy it from Backblaze: <b>Buckets</b>, then the <b>Endpoint</b> shown on your bucket, such as{" "}
                        <code className="font-mono">s3.us-east-005.backblazeb2.com</code>. Every bucket in an account shares one region. The region
                        below fills in by itself.
                      </>
                    ) : (
                      <>
                        For R2, use <code className="font-mono">&lt;ACCOUNT_ID&gt;.r2.cloudflarestorage.com</code> (Cloudflare dashboard → R2 →
                        Account details).
                      </>
                    )}
                  </FieldDescription>
                </Field>
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="s3-bucket">Bucket</FieldLabel>
                    <Input id="s3-bucket" required value={s3.bucket} onChange={(e) => set("bucket", e.target.value)} />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="s3-region">Region</FieldLabel>
                    <Input id="s3-region" value={s3.region} onChange={(e) => set("region", e.target.value)} />
                  </Field>
                </div>
                <Field>
                  <FieldLabel htmlFor="s3-prefix">Folder in the bucket (optional)</FieldLabel>
                  <Input id="s3-prefix" className="font-mono" placeholder="coffer/" value={s3.prefix} onChange={(e) => set("prefix", e.target.value)} />
                </Field>
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field>
                    <FieldLabel htmlFor="s3-ak">Access key ID</FieldLabel>
                    <Input id="s3-ak" required autoComplete="off" className="font-mono" value={s3.accessKey} onChange={(e) => set("accessKey", e.target.value)} />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="s3-sk">Secret access key</FieldLabel>
                    <Input
                      id="s3-sk"
                      type="password"
                      autoComplete="new-password"
                      required={!existing}
                      placeholder={existing ? "Leave blank to keep" : ""}
                      value={s3.secretKey ?? ""}
                      onChange={(e) => set("secretKey", e.target.value)}
                    />
                  </Field>
                </div>
                <FieldDescription>
                  {isB2
                    ? "In Backblaze, open Application Keys → Add a New Application Key, allow access to this bucket with Read and Write, then paste its keyID and applicationKey here. The applicationKey is shown only once. The secret is stored encrypted."
                    : "Create an R2 API token with Object Read & Write on this bucket only. The secret is stored encrypted."}
                </FieldDescription>
                <Field orientation="horizontal">
                  <div className="flex-1">
                    <FieldLabel htmlFor="s3-enc">Encrypt files</FieldLabel>
                    <FieldDescription>
                      {existing
                        ? s3.encrypt
                          ? "Files are encrypted before they leave this server. This is chosen when a drive is added."
                          : "Encryption is chosen when a drive is added. For an encrypted bucket, add a new drive with an empty folder."
                        : "Coffer encrypts files before they leave this server, so the provider only stores unreadable data. You still see and share them as usual on every device. Needs an empty bucket or folder."}
                    </FieldDescription>
                  </div>
                  <Switch
                    id="s3-enc"
                    checked={s3.encrypt}
                    disabled={!!existing}
                    onCheckedChange={(v) => setS3({ ...s3, encrypt: v, directDownloads: v ? false : s3.directDownloads })}
                  />
                </Field>
                {s3.encrypt && (
                  <Field>
                    <FieldLabel htmlFor="s3-rk">Recovery key</FieldLabel>
                    <Input
                      id="s3-rk"
                      autoComplete="off"
                      spellCheck={false}
                      className="font-mono"
                      placeholder={existing ? "Leave blank to keep" : "Leave blank for a new bucket folder"}
                      value={recoveryKey}
                      onChange={(e) => setRecoveryKey(e.target.value)}
                    />
                    <FieldDescription>
                      {existing
                        ? "Only needed if Coffer can't read the drive's key, for example after moving to a new server."
                        : "Only to reconnect a bucket folder that Coffer encrypted before, such as after moving to a new server."}
                    </FieldDescription>
                  </Field>
                )}
                <Field orientation="horizontal">
                  <div className="flex-1">
                    <FieldLabel htmlFor="s3-dd">Direct downloads</FieldLabel>
                    <FieldDescription>
                      {s3.encrypt
                        ? "Not available with encryption: the bucket only holds encrypted data, so downloads go through Coffer."
                        : "Downloads come straight from the bucket through short-lived signed links, which is faster for remote clients. R2 egress is free."}
                    </FieldDescription>
                  </div>
                  <Switch id="s3-dd" checked={s3.directDownloads && !s3.encrypt} disabled={s3.encrypt} onCheckedChange={(v) => set("directDownloads", v)} />
                </Field>
                <Field orientation="horizontal">
                  <div className="flex-1">
                    <FieldLabel htmlFor="s3-ps">Path-style addressing</FieldLabel>
                    <FieldDescription>Needed by MinIO and some self-hosted servers.</FieldDescription>
                  </div>
                  <Switch id="s3-ps" checked={s3.pathStyle} onCheckedChange={(v) => set("pathStyle", v)} />
                </Field>
                <Field orientation="horizontal">
                  <div className="flex-1">
                    <FieldLabel htmlFor="s3-in">Plain HTTP</FieldLabel>
                    <FieldDescription>Only for a server on your own network.</FieldDescription>
                  </div>
                  <Switch id="s3-in" checked={s3.insecure} onCheckedChange={(v) => set("insecure", v)} />
                </Field>
              </>
            )}
          </FieldGroup>
          {test && (
            <div
              className={cn(
                "flex gap-2 rounded-lg border p-3 text-sm",
                test.ok ? "border-emerald-500/40 bg-emerald-500/10" : "border-destructive/30 bg-destructive/5 text-destructive",
              )}
            >
              {test.ok ? <CheckCircle2Icon className="size-4 shrink-0 text-emerald-600" /> : <AlertCircleIcon className="size-4 shrink-0" />}
              {test.ok ? "Connection works: Coffer can list and write." : test.error}
            </div>
          )}
          <DialogFooter className="gap-2 sm:justify-between">
            <Button type="button" variant="outline" onClick={runTest} disabled={testing}>
              {testing && <Spinner />} Test connection
            </Button>
            <div className="flex gap-2">
              <Button type="button" variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                {busy && <Spinner />} {existing ? "Save" : "Add drive"}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ShowKeyDialog asks for the admin's password before showing a drive's
// recovery key again.
function ShowKeyDialog({ drive, onClose, onShown }: { drive: Drive | null; onClose: () => void; onShown: (d: Drive, key: string) => void }) {
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const close = () => {
    setPassword("")
    onClose()
  }

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    if (!drive) return
    setBusy(true)
    try {
      const { recoveryKey } = await api.driveRecoveryKey(drive.id, password)
      close()
      onShown(drive, recoveryKey)
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!drive} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>Recovery key for {drive?.name}</DialogTitle>
            <DialogDescription>Confirm with your password.</DialogDescription>
          </DialogHeader>
          <Input type="password" autoComplete="current-password" autoFocus required value={password} onChange={(e) => setPassword(e.target.value)} />
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !password}>
              {busy && <Spinner />} Show key
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// RecoveryKeyDialog shows a drive key to save. A new key has to be confirmed
// as saved before the dialog closes.
function RecoveryKeyDialog({ shown, onClose }: { shown: { name: string; key: string; fresh: boolean } | null; onClose: () => void }) {
  const [saved, setSaved] = useState(false)
  const [copied, setCopied] = useState(false)
  const canClose = !shown?.fresh || saved
  const close = () => {
    setSaved(false)
    setCopied(false)
    onClose()
  }
  const text = shown
    ? `Coffer recovery key for the drive "${shown.name}"\nYou need it to read this bucket's files if the Coffer server is lost.\n\n${shown.key}\n`
    : ""
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(shown?.key ?? "")
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error("Could not copy. Select the key and copy it manually.")
    }
  }
  const download = () => {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }))
    const a = document.createElement("a")
    a.href = url
    a.download = `coffer-recovery-key-${(shown?.name ?? "drive").replace(/[^\w.-]+/g, "-")}.txt`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Dialog open={!!shown} onOpenChange={(o) => !o && canClose && close()}>
      <DialogContent className="sm:max-w-lg" showCloseButton={canClose}>
        <DialogHeader>
          <DialogTitle>{shown?.fresh ? "Save your recovery key" : `Recovery key for ${shown?.name}`}</DialogTitle>
          <DialogDescription>
            Files on {shown?.name} are encrypted with this key. Coffer keeps a copy, but if this server or its data folder is lost, the files in
            the bucket (backups included) can only be read with it. Keep it in a password manager, away from this server.
          </DialogDescription>
        </DialogHeader>
        <div className="rounded-xl border bg-muted/40 p-4 font-mono text-sm break-all select-all">{shown?.key}</div>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={copy}>
            {copied ? <CheckIcon /> : <CopyIcon />} {copied ? "Copied" : "Copy key"}
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={download}>
            <DownloadIcon /> Download .txt
          </Button>
        </div>
        {shown?.fresh && (
          <div className="flex items-center gap-2">
            <Checkbox id="key-saved" checked={saved} onCheckedChange={(v) => setSaved(v === true)} />
            <Label htmlFor="key-saved" className="font-normal">
              I saved the recovery key
            </Label>
          </div>
        )}
        <DialogFooter>
          <Button disabled={!canClose} onClick={close}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
