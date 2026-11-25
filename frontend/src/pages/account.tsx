import { useEffect, useState, type FormEvent, type ReactNode } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  AppleIcon,
  CheckIcon,
  CopyIcon,
  DownloadIcon,
  KeyRoundIcon,
  LaptopIcon,
  LayoutGridIcon,
  ListIcon,
  MonitorIcon,
  MoonIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  ShieldIcon,
  SunIcon,
  TerminalIcon,
  Trash2Icon,
} from "lucide-react"
import QRCode from "qrcode"
import { useSearchParams } from "react-router"
import { toast } from "sonner"

import { useConfig } from "@/App"
import { useUser } from "@/components/app-layout"
import { Page } from "@/components/page"
import { EmptyState, Spinner } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api, type Me } from "@/lib/api"
import { formatBytes, formatDate, relativeTime } from "@/lib/format"
import { usePrefs } from "@/lib/prefs"
import { cn } from "@/lib/utils"

const TABS = ["general", "security", "connect", "appearance"] as const
type Tab = (typeof TABS)[number]

export default function AccountPage() {
  const me = useUser()
  const { data: config } = useConfig()
  const [params, setParams] = useSearchParams()
  const enrolling = params.get("enroll") === "1" || me.mustEnroll2fa
  const raw = params.get("tab") as Tab | null
  const tab: Tab = enrolling ? "security" : raw && TABS.includes(raw) ? raw : "general"

  return (
    <Page title="User settings" description={`Signed in as ${me.username}`}>
      <Tabs value={tab} onValueChange={(v) => setParams({ tab: v }, { replace: true })} className="gap-6">
        <div className="-mx-4 overflow-x-auto px-4 md:mx-0 md:px-0">
          <TabsList variant="chips">
            <TabsTrigger value="general" disabled={enrolling}>
              General
            </TabsTrigger>
            <TabsTrigger value="security">Security</TabsTrigger>
            {config?.webdav !== false && (
              <TabsTrigger value="connect" disabled={enrolling}>
                Connect apps
              </TabsTrigger>
            )}
            <TabsTrigger value="appearance" disabled={enrolling}>
              Appearance
            </TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="general" className="space-y-6">
          <GeneralTab />
        </TabsContent>
        <TabsContent value="security" className="space-y-6">
          {enrolling && !me.totpEnabled && (
            <div className="flex gap-3 rounded-xl border border-amber-500/40 bg-amber-500/10 p-4">
              <ShieldAlertIcon className="size-5 shrink-0 text-amber-600 dark:text-amber-400" />
              <div>
                <div className="font-semibold">Two-factor authentication is required</div>
                <p className="text-sm text-muted-foreground">
                  Your administrator requires two-factor authentication. Set it up below to continue using{" "}
                  {config?.appName ?? "Coffer"}.
                </p>
              </div>
            </div>
          )}
          <TwoFactorCard />
          <PasswordCard />
          <SessionsCard />
        </TabsContent>
        <TabsContent value="connect" className="space-y-6">
          <ConnectTab />
        </TabsContent>
        <TabsContent value="appearance" className="space-y-6">
          <AppearanceTab />
        </TabsContent>
      </Tabs>
    </Page>
  )
}

function SettingsCard({ title, description, children, action }: { title: ReactNode; description?: ReactNode; children: ReactNode; action?: ReactNode }) {
  return (
    <Card className="rounded-xl shadow-none">
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2">{title}</CardTitle>
          {description && <CardDescription>{description}</CardDescription>}
        </div>
        {action}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  )
}

function useCopy() {
  const [copied, setCopied] = useState(false)
  return {
    copied,
    copy: async (text: string) => {
      try {
        await navigator.clipboard.writeText(text)
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      } catch {
        toast.error("Could not copy. Select the text and copy it manually.")
      }
    },
  }
}

function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const { copied, copy } = useCopy()
  return (
    <Button type="button" variant="outline" size="sm" onClick={() => copy(text)}>
      {copied ? <CheckIcon /> : <CopyIcon />} {copied ? "Copied" : label}
    </Button>
  )
}

/* General */

function GeneralTab() {
  const me = useUser()
  return (
    <>
      <ProfileCard />
      <NotificationsCard />
      {me.quotaBytes > 0 && <StorageCard />}
    </>
  )
}

function ProfileCard() {
  const me = useUser()
  return (
    <SettingsCard title="Profile" description="Your account details. Ask an administrator to change your username or role.">
      <dl className="grid gap-4 text-sm sm:grid-cols-2">
        <div>
          <dt className="text-muted-foreground">Username</dt>
          <dd className="font-medium">{me.username}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Role</dt>
          <dd className="font-medium capitalize">{me.role}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Two-factor authentication</dt>
          <dd className="font-medium">{me.totpEnabled ? "On" : "Off"}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Member since</dt>
          <dd className="font-medium">{formatDate(me.createdAt)}</dd>
        </div>
      </dl>
    </SettingsCard>
  )
}

function NotificationsCard() {
  const me = useUser()
  const qc = useQueryClient()
  const [prefs, setPrefs] = usePrefs()
  const [email, setEmail] = useState(me.email ?? "")
  const [busy, setBusy] = useState(false)

  useEffect(() => setEmail(me.email ?? ""), [me.email])

  const save = async (ev: FormEvent) => {
    ev.preventDefault()
    setBusy(true)
    try {
      const updated = await api.setEmail(email.trim())
      qc.setQueryData(["me"], updated)
      await qc.invalidateQueries({ queryKey: ["me"] })
      toast.success(email.trim() ? "Email address saved" : "Email address removed")
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <SettingsCard
      title="Notifications"
      description="Get an email when someone opens, downloads from, uploads to or comments on your links. It is also the reply-to address on invitations you send."
    >
      <form onSubmit={save}>
        <FieldGroup className="gap-4">
          <Field>
            <FieldLabel htmlFor="me-email">Email address</FieldLabel>
            <div className="flex gap-2">
              <Input id="me-email" type="email" autoComplete="email" placeholder="you@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
              <Button type="submit" variant="outline" disabled={busy || email.trim() === (me.email ?? "")}>
                {busy && <Spinner />} Save
              </Button>
            </div>
            {!me.mailReady && <FieldDescription>Email isn't set up on this server yet, so nothing will be sent until an administrator adds it.</FieldDescription>}
          </Field>
          <Field orientation="horizontal">
            <div className="flex-1">
              <FieldLabel htmlFor="me-notify">Email me notifications</FieldLabel>
              <FieldDescription>They always appear in the app too.</FieldDescription>
            </div>
            <Switch id="me-notify" checked={prefs.emailNotify} disabled={!me.email} onCheckedChange={(v) => setPrefs({ emailNotify: v })} />
          </Field>
        </FieldGroup>
      </form>
    </SettingsCard>
  )
}

function StorageCard() {
  const me = useUser()
  const pct = Math.min(100, (me.quotaUsed / me.quotaBytes) * 100)
  return (
    <SettingsCard title="Storage" description="Your home folders count towards this limit, including files others upload to them.">
      <div className="mb-2 flex items-baseline justify-between text-sm">
        <span className="font-medium">
          {formatBytes(me.quotaUsed)} of {formatBytes(me.quotaBytes)}
        </span>
        <span className="text-muted-foreground tabular-nums">{Math.round(pct)}%</span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-secondary">
        <div className={cn("h-full rounded-full", pct > 90 ? "bg-destructive" : "bg-brand")} style={{ width: `${pct}%` }} />
      </div>
    </SettingsCard>
  )
}

/* Security */

function PasswordCard() {
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [busy, setBusy] = useState(false)

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    if (next !== confirm) return toast.error("The new passwords do not match")
    setBusy(true)
    try {
      const res = await api.changePassword(current, next)
      const n = res.appPasswordsRevoked
      toast.success(
        n > 0
          ? `Password changed. Other devices were signed out and ${n} app password${n === 1 ? " was" : "s were"} revoked.`
          : "Password changed. Other devices were signed out.",
      )
      setCurrent("")
      setNext("")
      setConfirm("")
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <SettingsCard
      title="Password"
      description="Use at least 10 characters. A passphrase of several words works well. Changing it signs out your other devices and revokes your app passwords."
    >
      <form onSubmit={submit}>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="cur">Current password</FieldLabel>
            <Input id="cur" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="new">New password</FieldLabel>
              <Input id="new" type="password" autoComplete="new-password" minLength={10} required value={next} onChange={(e) => setNext(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="confirm">Confirm new password</FieldLabel>
              <Input id="confirm" type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
            </Field>
          </div>
          <div>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner />} Change password
            </Button>
          </div>
        </FieldGroup>
      </form>
    </SettingsCard>
  )
}

function RecoveryCodesView({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const [saved, setSaved] = useState(false)
  const text = `Coffer recovery codes\nEach code works once.\n\n${codes.join("\n")}\n`
  const download = () => {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }))
    const a = document.createElement("a")
    a.href = url
    a.download = "coffer-recovery-codes.txt"
    a.click()
    URL.revokeObjectURL(url)
  }
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Save these recovery codes somewhere safe, such as a password manager. Each works once, if you lose your authenticator
        app. <span className="font-medium text-foreground">They will not be shown again.</span>
      </p>
      <div className="grid grid-cols-2 gap-2 rounded-xl border bg-muted/40 p-4 font-mono text-sm sm:grid-cols-5">
        {codes.map((c) => (
          <span key={c}>{c}</span>
        ))}
      </div>
      <div className="flex flex-wrap gap-2">
        <CopyButton text={codes.join("\n")} label="Copy codes" />
        <Button type="button" variant="outline" size="sm" onClick={download}>
          <DownloadIcon /> Download .txt
        </Button>
      </div>
      <div className="flex items-center gap-2">
        <Checkbox id="saved" checked={saved} onCheckedChange={(v) => setSaved(v === true)} />
        <Label htmlFor="saved" className="font-normal">
          I saved my recovery codes
        </Label>
      </div>
      <Button disabled={!saved} onClick={onDone}>
        Done
      </Button>
    </div>
  )
}

function TwoFactorCard() {
  const me = useUser()
  const qc = useQueryClient()
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null)
  const [qr, setQr] = useState("")
  const [code, setCode] = useState("")
  const [codes, setCodes] = useState<string[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [pwDialog, setPwDialog] = useState<"setup" | "disable" | "regenerate" | null>(null)
  const [password, setPassword] = useState("")
  // Setup asks for the password once; enabling sends it again.
  const [setupPassword, setSetupPassword] = useState("")

  useEffect(() => {
    if (setup) QRCode.toDataURL(setup.uri, { margin: 1, width: 200 }).then(setQr)
  }, [setup])

  const enable = async (value = code) => {
    setBusy(true)
    try {
      const res = await api.totpEnable(value, setupPassword)
      qc.setQueryData<Me>(["me"], res.me)
      setSetup(null)
      setSetupPassword("")
      setCodes(res.recoveryCodes)
      const apps = res.appPasswords ?? []
      if (apps.length > 0) {
        toast.success("Two-factor authentication is on", {
          description: `App passwords still work without a code: ${apps.join(", ")}. Remove any you don't recognise under Connect apps.`,
          duration: 15000,
        })
      } else {
        toast.success("Two-factor authentication is on")
      }
    } catch (e) {
      toast.error((e as Error).message)
      setCode("")
    } finally {
      setBusy(false)
    }
  }

  const confirmPassword = async (ev: FormEvent) => {
    ev.preventDefault()
    setBusy(true)
    try {
      if (pwDialog === "setup") {
        setSetup(await api.totpSetup(password))
        setSetupPassword(password)
        setCode("")
      } else if (pwDialog === "disable") {
        qc.setQueryData<Me>(["me"], await api.totpDisable(password))
        toast.success("Two-factor authentication is off")
      } else {
        const res = await api.recoveryCodes(password)
        setCodes(res.recoveryCodes)
      }
      setPwDialog(null)
      setPassword("")
      qc.invalidateQueries({ queryKey: ["me"] })
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const finishCodes = () => {
    setCodes(null)
    qc.invalidateQueries({ queryKey: ["me"] })
  }

  return (
    <SettingsCard
      title={
        <>
          Two-factor authentication
          {me.totpEnabled ? (
            <Badge className="bg-emerald-600 text-white">
              <ShieldCheckIcon /> On
            </Badge>
          ) : (
            <Badge variant="outline">
              <ShieldIcon /> Off
            </Badge>
          )}
        </>
      }
      description="Ask for a code from an authenticator app (1Password, Proton Pass, Google Authenticator…) when signing in."
    >
      {codes ? (
        <RecoveryCodesView codes={codes} onDone={finishCodes} />
      ) : me.totpEnabled ? (
        <div className="space-y-4">
          <div className="flex items-center gap-3 rounded-lg border p-3">
            <KeyRoundIcon className="size-5 text-muted-foreground" />
            <div className="flex-1 text-sm">
              <div className="font-medium">Recovery codes</div>
              <div className="text-muted-foreground">
                {me.recoveryLeft} of 10 left{me.recoveryLeft <= 3 && " — generate new ones soon"}
              </div>
            </div>
            <Button variant="outline" size="sm" onClick={() => setPwDialog("regenerate")}>
              Generate new codes
            </Button>
          </div>
          <Button variant="destructive" onClick={() => setPwDialog("disable")}>
            Turn off two-factor
          </Button>
        </div>
      ) : setup ? (
        <div className="flex flex-col gap-6 sm:flex-row">
          {qr && <img src={qr} alt="Scan with your authenticator app" className="size-44 shrink-0 rounded-lg border bg-white p-2" />}
          <div className="min-w-0 space-y-4">
            <p className="text-sm text-muted-foreground">
              Scan the code with your authenticator app, or enter this key manually:
              <code className="mt-1 block rounded-md bg-muted px-2 py-1 font-mono text-xs break-all text-foreground">{setup.secret}</code>
            </p>
            <Field>
              <FieldLabel>Then enter the 6-digit code it shows</FieldLabel>
              <InputOTP
                maxLength={6}
                value={code}
                disabled={busy}
                onChange={(v) => {
                  setCode(v)
                  if (v.length === 6) enable(v)
                }}
              >
                <InputOTPGroup>
                  {Array.from({ length: 6 }).map((_, i) => (
                    <InputOTPSlot key={i} index={i} />
                  ))}
                </InputOTPGroup>
              </InputOTP>
            </Field>
            <Button
              variant="ghost"
              onClick={() => {
                setSetup(null)
                setSetupPassword("")
              }}
              disabled={busy}
            >
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <Button onClick={() => setPwDialog("setup")}>Set up two-factor</Button>
      )}

      <Dialog open={!!pwDialog} onOpenChange={(o) => !o && setPwDialog(null)}>
        <DialogContent className="sm:max-w-md">
          <form onSubmit={confirmPassword} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>
                {pwDialog === "setup" ? "Set up two-factor" : pwDialog === "disable" ? "Turn off two-factor?" : "Generate new recovery codes?"}
              </DialogTitle>
              <DialogDescription>
                {pwDialog === "setup"
                  ? "You'll scan a code with your authenticator app next."
                  : pwDialog === "disable"
                    ? "Your account will be protected by your password only."
                    : "Your old recovery codes will stop working."}{" "}
                Confirm with your password.
              </DialogDescription>
            </DialogHeader>
            <Input type="password" autoComplete="current-password" autoFocus required value={password} onChange={(e) => setPassword(e.target.value)} />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setPwDialog(null)}>
                Cancel
              </Button>
              <Button type="submit" variant={pwDialog === "disable" ? "destructive" : "default"} disabled={busy}>
                {busy && <Spinner />} {pwDialog === "setup" ? "Continue" : pwDialog === "disable" ? "Turn off" : "Generate"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </SettingsCard>
  )
}

function describeAgent(ua: string) {
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser"
  const os = /iPhone|iPad/.test(ua) ? "iOS" : /Mac OS X/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Android/.test(ua) ? "Android" : /Linux/.test(ua) ? "Linux" : ""
  return os ? `${browser} on ${os}` : browser
}

function SessionsCard() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ["sessions"], queryFn: api.sessions })
  const revoke = async (id: string) => {
    await api.revokeSession(id).catch((e) => toast.error(e.message))
    qc.invalidateQueries({ queryKey: ["sessions"] })
  }
  return (
    <SettingsCard title="Signed-in devices" description="Sign out anything you do not recognise, then change your password.">
      {isLoading ? (
        <Spinner />
      ) : (
        <div className="divide-y">
          {(data ?? []).map((s) => (
            <div key={s.id} className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
              <div className="flex size-10 shrink-0 items-center justify-center rounded-[10px] border bg-secondary/70">
                <LaptopIcon className="size-5 text-muted-foreground" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{describeAgent(s.userAgent)}</div>
                <div className="truncate text-xs text-muted-foreground">
                  {s.ip} · active {relativeTime(s.lastSeen)}
                </div>
              </div>
              {s.current ? (
                <Badge variant="secondary">This device</Badge>
              ) : (
                <Button variant="outline" size="sm" onClick={() => revoke(s.id)}>
                  Sign out
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
    </SettingsCard>
  )
}

/* Connect apps (WebDAV) */

function OsBlock({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <div className="rounded-xl border p-4">
      <div className="mb-2 flex items-center gap-2 font-semibold">
        {icon}
        {title}
      </div>
      <div className="space-y-1.5 text-sm text-muted-foreground">{children}</div>
    </div>
  )
}

function Code({ children }: { children: ReactNode }) {
  return <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all text-foreground">{children}</code>
}

function ConnectTab() {
  const me = useUser()
  const { data: config } = useConfig()
  const qc = useQueryClient()
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: api.tokens, enabled: config?.webdav !== false })
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<{ secret: string; username: string; name: string } | null>(null)

  if (config && !config.webdav) {
    return <EmptyState icon={<MonitorIcon />} title="Desktop connections are turned off" description="An administrator can enable WebDAV in App settings." />
  }

  const origin = window.location.origin
  const host = window.location.host
  const url = `${origin}/dav/`
  const secure = window.location.protocol === "https:"

  const create = async (ev: FormEvent) => {
    ev.preventDefault()
    setBusy(true)
    try {
      const res = await api.createToken(name, password)
      setCreated({ secret: res.secret, username: res.username, name })
      setName("")
      setPassword("")
      qc.invalidateQueries({ queryKey: ["tokens"] })
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const revoke = async (id: string) => {
    await api.deleteToken(id).catch((e) => toast.error(e.message))
    qc.invalidateQueries({ queryKey: ["tokens"] })
  }

  return (
    <>
      <SettingsCard
        title="Connect from your desktop"
        description="Mount your files in Finder, Explorer or your Linux file manager over WebDAV. Sign in with your username and an app password, not your account password."
      >
        <div className="mb-5 flex flex-wrap items-center gap-2 rounded-lg border bg-muted/40 p-3">
          <span className="text-sm text-muted-foreground">Server address</span>
          <Code>{url}</Code>
          <CopyButton text={url} />
        </div>
        {!secure && (
          <p className="mb-5 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
            This page is not on HTTPS. Desktop clients send your app password with every request, so connect through an HTTPS
            address (a reverse proxy or Tailscale) when outside your local network.
          </p>
        )}
        <div className="grid gap-3 md:grid-cols-2">
          <OsBlock icon={<AppleIcon className="size-4" />} title="macOS Finder">
            <p>
              Press <Code>⌘K</Code> (Go → Connect to Server), enter <Code>{url}</Code> and choose Connect.
            </p>
            <p>Sign in as {me.username} with an app password. Tick “Remember this password in my keychain”.</p>
          </OsBlock>
          <OsBlock icon={<MonitorIcon className="size-4" />} title="Windows Explorer">
            <p>Right-click This PC → Map network drive, and use:</p>
            <p>
              <Code>{secure ? `\\\\${host}@SSL\\dav` : `\\\\${host}\\dav`}</Code>
            </p>
            <p>Tick “Connect using different credentials”.</p>
          </OsBlock>
          <OsBlock icon={<LaptopIcon className="size-4" />} title="Linux (Nautilus, Dolphin)">
            <p>
              Other Locations → Connect to Server: <Code>{`${secure ? "davs" : "dav"}://${host}/dav/`}</Code>
            </p>
          </OsBlock>
          <OsBlock icon={<TerminalIcon className="size-4" />} title="rclone">
            <p>
              <Code>rclone config</Code> → type <Code>webdav</Code>, url <Code>{url}</Code>, vendor <Code>other</Code>, your username and an app password.
            </p>
          </OsBlock>
        </div>
        <p className="mt-4 text-sm text-muted-foreground">
          Tip: for heavy media work such as After Effects projects on the studio network, the SMB share (for example{" "}
          <Code>smb://server/Files</Code>) is faster than WebDAV. Ask your administrator for its address.
        </p>
      </SettingsCard>

      <SettingsCard title="App passwords" description="One per device or app, so you can revoke a lost laptop without changing your password.">
        <form onSubmit={create} className="mb-5 grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
          <Field>
            <FieldLabel htmlFor="tok-name">Name</FieldLabel>
            <Input id="tok-name" required maxLength={60} placeholder="Studio Mac Finder" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor="tok-pw">Your current password</FieldLabel>
            <Input id="tok-pw" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Button type="submit" disabled={busy}>
            {busy && <Spinner />} Create
          </Button>
        </form>
        {tokens.data?.length ? (
          <div className="divide-y rounded-xl border">
            {tokens.data.map((t) => (
              <div key={t.id} className="flex items-center gap-3 p-3">
                <div className="flex size-10 shrink-0 items-center justify-center rounded-[10px] border bg-secondary/70">
                  <KeyRoundIcon className="size-5 text-muted-foreground" />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">{t.name}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    Created {formatDate(t.createdAt)} · {t.lastUsed ? `last used ${relativeTime(t.lastUsed)} from ${t.lastIp}` : "never used"}
                  </div>
                </div>
                <Button variant="ghost" size="icon-sm" onClick={() => revoke(t.id)} aria-label={`Revoke ${t.name}`}>
                  <Trash2Icon />
                </Button>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">No app passwords yet.</p>
        )}
      </SettingsCard>

      <Dialog open={!!created} onOpenChange={(o) => !o && setCreated(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>App password for “{created?.name}”</DialogTitle>
            <DialogDescription>Copy it now. It will not be shown again.</DialogDescription>
          </DialogHeader>
          {created && (
            <div className="space-y-3 text-sm">
              <div className="flex items-center justify-between gap-2 rounded-lg border p-3">
                <div>
                  <div className="text-xs text-muted-foreground">Username</div>
                  <div className="font-mono">{created.username}</div>
                </div>
                <CopyButton text={created.username} />
              </div>
              <div className="flex items-center justify-between gap-2 rounded-lg border p-3">
                <div className="min-w-0">
                  <div className="text-xs text-muted-foreground">Password</div>
                  <div className="font-mono break-all">{created.secret}</div>
                </div>
                <CopyButton text={created.secret} />
              </div>
            </div>
          )}
          <DialogFooter>
            <Button onClick={() => setCreated(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

/* Appearance */

function ChoiceCard({ active, onClick, icon, label }: { active: boolean; onClick: () => void; icon: ReactNode; label: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "flex flex-1 flex-col items-center gap-2 rounded-xl border bg-background p-4 text-sm font-medium transition-colors hover:bg-muted/60",
        active && "border-brand ring-2 ring-brand/30",
      )}
    >
      {icon}
      {label}
    </button>
  )
}

function AppearanceTab() {
  const [prefs, setPrefs] = usePrefs()
  const { data: config } = useConfig()
  const accent = prefs.accent || config?.accentColor || "#3b5bdb"
  return (
    <>
      <SettingsCard title="Theme" description="Follow your system, or pick one.">
        <div className="flex flex-col gap-3 sm:flex-row">
          <ChoiceCard active={prefs.theme === "light"} onClick={() => setPrefs({ theme: "light" })} icon={<SunIcon className="size-5" />} label="Light" />
          <ChoiceCard active={prefs.theme === "dark"} onClick={() => setPrefs({ theme: "dark" })} icon={<MoonIcon className="size-5" />} label="Dark" />
          <ChoiceCard active={prefs.theme === "system"} onClick={() => setPrefs({ theme: "system" })} icon={<MonitorIcon className="size-5" />} label="System" />
        </div>
      </SettingsCard>
      <SettingsCard title="Layout">
        <FieldGroup>
          <Field orientation="horizontal">
            <div className="flex-1">
              <FieldLabel>Density</FieldLabel>
              <FieldDescription>Compact fits more rows on screen.</FieldDescription>
            </div>
            <Select value={prefs.density} onValueChange={(v) => setPrefs({ density: v as "comfortable" | "compact" })}>
              <SelectTrigger className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="comfortable">Comfortable</SelectItem>
                <SelectItem value="compact">Compact</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field orientation="horizontal">
            <div className="flex-1">
              <FieldLabel>Default view</FieldLabel>
            </div>
            <div className="flex gap-2">
              <Button type="button" variant={prefs.view === "list" ? "default" : "outline"} size="sm" onClick={() => setPrefs({ view: "list" })}>
                <ListIcon /> List
              </Button>
              <Button type="button" variant={prefs.view === "grid" ? "default" : "outline"} size="sm" onClick={() => setPrefs({ view: "grid" })}>
                <LayoutGridIcon /> Grid
              </Button>
            </div>
          </Field>
          <Field orientation="horizontal">
            <div className="flex-1">
              <FieldLabel>Rows per page</FieldLabel>
            </div>
            <Select value={String(prefs.pageSize)} onValueChange={(v) => setPrefs({ pageSize: Number(v) })}>
              <SelectTrigger className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {[25, 50, 100].map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field orientation="horizontal">
            <div className="flex-1">
              <FieldLabel htmlFor="qc">Show quick-create cards</FieldLabel>
              <FieldDescription>New document, folder and upload shortcuts above your files.</FieldDescription>
            </div>
            <Switch id="qc" checked={prefs.quickCreate} onCheckedChange={(v) => setPrefs({ quickCreate: v })} />
          </Field>
          <Field orientation="horizontal">
            <div className="flex-1">
              <FieldLabel htmlFor="hidden">Show hidden files</FieldLabel>
              <FieldDescription>Dot-folders like .backups and system files, in your own view only. Share links never show them.</FieldDescription>
            </div>
            <Switch id="hidden" checked={prefs.showHidden} onCheckedChange={(v) => setPrefs({ showHidden: v })} />
          </Field>
        </FieldGroup>
      </SettingsCard>
      <SettingsCard title="Accent colour" description="Overrides your organisation's colour for you only.">
        <div className="flex flex-wrap items-center gap-3">
          <input
            type="color"
            aria-label="Accent colour"
            value={accent}
            onChange={(e) => setPrefs({ accent: e.target.value })}
            className="h-10 w-14 cursor-pointer rounded-lg border bg-transparent p-1"
          />
          <Input value={accent} onChange={(e) => /^#[0-9a-f]{6}$/i.test(e.target.value) && setPrefs({ accent: e.target.value })} className="w-32 font-mono" />
          <Button type="button" variant="outline" size="sm" disabled={!prefs.accent} onClick={() => setPrefs({ accent: "" })}>
            Reset to default
          </Button>
        </div>
      </SettingsCard>
    </>
  )
}
