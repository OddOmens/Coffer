import { useEffect, useRef, useState, type ChangeEvent, type FormEvent, type ReactNode } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { CheckCircle2Icon, CircleAlertIcon, ImageUpIcon, MailIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { useConfig } from "@/App"
import { BrandMark } from "@/components/brand"
import { AdminPage } from "@/components/admin-page"
import { Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { api, type Settings } from "@/lib/api"
import { applyAccent } from "@/lib/branding"

function SettingsCard({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <Card className="rounded-xl shadow-none">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
      </CardHeader>
      <CardContent>
        <FieldGroup>{children}</FieldGroup>
      </CardContent>
    </Card>
  )
}

function Toggle({ id, label, description, checked, onChange }: { id: string; label: string; description?: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <Field orientation="horizontal">
      <div className="flex-1">
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        {description && <FieldDescription>{description}</FieldDescription>}
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </Field>
  )
}

export default function SettingsPage() {
  const qc = useQueryClient()
  const { data: config } = useConfig()
  const { data } = useQuery({ queryKey: ["settings"], queryFn: api.settings })
  const [form, setForm] = useState<Settings | null>(null)
  const [busy, setBusy] = useState(false)
  // undefined = leave the saved SMTP password alone; "" = remove it.
  const [smtpPassword, setSmtpPassword] = useState<string | undefined>(undefined)
  const [testTo, setTestTo] = useState("")
  const [testing, setTesting] = useState(false)
  const logoInput = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (data) setForm(data)
  }, [data])

  // Put the saved accent back if the page is left with unsaved changes.
  useEffect(() => () => applyAccent(qc.getQueryData<{ accentColor: string }>(["config"])?.accentColor ?? ""), [qc])

  if (!form) {
    return (
      <AdminPage>
        <Skeleton className="h-96 w-full rounded-xl" />
      </AdminPage>
    )
  }

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setForm({ ...form, [k]: v })
  const num = (k: keyof Settings) => (e: ChangeEvent<HTMLInputElement>) => set(k, Math.max(0, Number(e.target.value) || 0) as never)

  const save = async (ev?: FormEvent) => {
    ev?.preventDefault()
    setBusy(true)
    try {
      const saved = await api.saveSettings({ ...form, smtpPassword })
      qc.setQueryData(["settings"], saved)
      setForm(saved)
      setSmtpPassword(undefined)
      qc.invalidateQueries({ queryKey: ["me"] })
      await qc.invalidateQueries({ queryKey: ["config"] })
      toast.success("Settings saved")
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const sendTest = async () => {
    if (!testTo.trim()) return
    setTesting(true)
    try {
      await api.testEmail(testTo.trim())
      toast.success(`Test email sent to ${testTo.trim()}`)
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setTesting(false)
    }
  }

  const uploadLogo = async (file?: File) => {
    if (!file) return
    try {
      await api.uploadLogo(file)
      await qc.invalidateQueries({ queryKey: ["config"] })
      qc.invalidateQueries({ queryKey: ["settings"] })
      toast.success("Logo updated")
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  const removeLogo = async () => {
    await api.deleteLogo().catch((e) => toast.error(e.message))
    await qc.invalidateQueries({ queryKey: ["config"] })
    qc.invalidateQueries({ queryKey: ["settings"] })
  }

  return (
    <AdminPage
      description={`Coffer ${config?.version ?? ""}`}
      actions={
        <Button onClick={() => save()} disabled={busy}>
          {busy && <Spinner />} Save changes
        </Button>
      }
    >
      <form onSubmit={save} className="grid gap-6">
        <SettingsCard title="Branding" description="Shown on the sign-in page, in the app and on every public link page.">
          <div className="flex flex-wrap items-center gap-4">
            <BrandMark className="size-14 rounded-xl" />
            <div className="flex gap-2">
              <Button type="button" variant="outline" onClick={() => logoInput.current?.click()}>
                <ImageUpIcon /> Upload logo
              </Button>
              {config?.hasLogo && (
                <Button type="button" variant="ghost" onClick={removeLogo}>
                  <Trash2Icon /> Remove
                </Button>
              )}
            </div>
            <input
              ref={logoInput}
              type="file"
              accept="image/png,image/jpeg,image/webp,image/svg+xml"
              hidden
              onChange={(e) => {
                uploadLogo(e.target.files?.[0])
                e.target.value = ""
              }}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-[1fr_13rem]">
            <Field>
              <FieldLabel htmlFor="appName">Name</FieldLabel>
              <Input id="appName" value={form.appName} maxLength={60} onChange={(e) => set("appName", e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="accent">Accent colour</FieldLabel>
              <div className="flex gap-2">
                <input
                  id="accent"
                  type="color"
                  value={form.accentColor}
                  onChange={(e) => {
                    set("accentColor", e.target.value)
                    applyAccent(e.target.value)
                  }}
                  className="h-9 w-11 cursor-pointer rounded-lg border bg-transparent p-0.5"
                />
                <Input value={form.accentColor} onChange={(e) => set("accentColor", e.target.value)} className="font-mono" />
              </div>
            </Field>
          </div>
          <Field>
            <FieldLabel htmlFor="loginMessage">Sign-in message</FieldLabel>
            <Textarea id="loginMessage" rows={2} maxLength={500} value={form.loginMessage} onChange={(e) => set("loginMessage", e.target.value)} />
          </Field>
        </SettingsCard>

        <SettingsCard title="Appearance defaults" description="What people see until they choose their own in Settings → Appearance.">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>Theme</FieldLabel>
              <Select value={form.defaultTheme} onValueChange={(v) => set("defaultTheme", v as Settings["defaultTheme"])}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="system">Follow the system</SelectItem>
                  <SelectItem value="light">Light</SelectItem>
                  <SelectItem value="dark">Dark</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel>Density</FieldLabel>
              <Select value={form.defaultDensity} onValueChange={(v) => set("defaultDensity", v as Settings["defaultDensity"])}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="comfortable">Comfortable</SelectItem>
                  <SelectItem value="compact">Compact</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </div>
          <Toggle
            id="qc"
            label="Show quick-create cards"
            description="New document, folder and upload shortcuts above the file list."
            checked={form.showQuickCreate}
            onChange={(v) => set("showQuickCreate", v)}
          />
        </SettingsCard>

        <SettingsCard title="Security">
          <Field>
            <FieldLabel>Require two-factor authentication</FieldLabel>
            <FieldDescription>People without two-factor are asked to set it up before they can use their files.</FieldDescription>
            <RadioGroup value={form.require2fa} onValueChange={(v) => set("require2fa", v as Settings["require2fa"])} className="mt-2 gap-3">
              {[
                ["off", "Optional", "Everyone decides for themselves."],
                ["admins", "Administrators", "Required for admin accounts."],
                ["all", "Everyone", "Required for every account."],
              ].map(([v, label, help]) => (
                <div key={v} className="flex items-start gap-3 rounded-lg border p-3">
                  <RadioGroupItem value={v} id={`2fa-${v}`} className="mt-0.5" />
                  <Label htmlFor={`2fa-${v}`} className="grid gap-0.5 font-normal">
                    <span className="font-medium">{label}</span>
                    <span className="text-muted-foreground">{help}</span>
                  </Label>
                </div>
              ))}
            </RadioGroup>
          </Field>
        </SettingsCard>

        <SettingsCard title="Share links" description="Rules for every new or edited link.">
          <Toggle id="reqpw" label="Require a password on every link" checked={form.shareRequirePassword} onChange={(v) => set("shareRequirePassword", v)} />
          <div className="grid gap-4 sm:grid-cols-3">
            <Field>
              <FieldLabel htmlFor="defexp">Default expiry (days)</FieldLabel>
              <Input id="defexp" type="number" min={0} value={form.shareDefaultExpiryDays} onChange={num("shareDefaultExpiryDays")} />
              <FieldDescription>0 = never</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="maxexp">Maximum expiry (days)</FieldLabel>
              <Input id="maxexp" type="number" min={0} value={form.shareMaxExpiryDays} onChange={num("shareMaxExpiryDays")} />
              <FieldDescription>0 = no limit</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="upmax">Upload limit per file (MB)</FieldLabel>
              <Input id="upmax" type="number" min={0} value={form.shareUploadMaxMB} onChange={num("shareUploadMaxMB")} />
              <FieldDescription>For upload links. 0 = no limit</FieldDescription>
            </Field>
          </div>
        </SettingsCard>

        <SettingsCard
          title="Email"
          description="For link invitations, sign-in codes and notifications. Any SMTP service works: a Proton SMTP token, Postmark, Resend, Amazon SES."
        >
          <div className="flex items-center gap-2 text-sm">
            {data?.mailReady ? (
              <>
                <CheckCircle2Icon className="size-4 text-emerald-600" /> Email is set up.
              </>
            ) : (
              <>
                <CircleAlertIcon className="size-4 text-amber-500" /> Not set up yet. Links that ask for an email address need it.
              </>
            )}
          </div>
          <div className="grid gap-4 sm:grid-cols-[1fr_7rem_10rem]">
            <Field>
              <FieldLabel htmlFor="smtpHost">Mail server</FieldLabel>
              <Input id="smtpHost" placeholder="smtp.example.com" value={form.smtpHost} onChange={(e) => set("smtpHost", e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="smtpPort">Port</FieldLabel>
              <Input id="smtpPort" type="number" min={1} max={65535} value={form.smtpPort} onChange={num("smtpPort")} />
            </Field>
            <Field>
              <FieldLabel>Security</FieldLabel>
              <Select
                value={form.smtpSecurity}
                onValueChange={(v) => {
                  const sec = v as Settings["smtpSecurity"]
                  setForm({ ...form, smtpSecurity: sec, smtpPort: sec === "tls" ? 465 : sec === "starttls" && form.smtpPort === 465 ? 587 : form.smtpPort })
                }}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="starttls">STARTTLS</SelectItem>
                  <SelectItem value="tls">TLS</SelectItem>
                  <SelectItem value="none">None</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="smtpUser">Username</FieldLabel>
              <Input id="smtpUser" autoComplete="off" value={form.smtpUser} onChange={(e) => set("smtpUser", e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="smtpPass">Password or token</FieldLabel>
              <div className="flex gap-2">
                <Input
                  id="smtpPass"
                  type="password"
                  autoComplete="new-password"
                  placeholder={form.smtpHasPassword && smtpPassword === undefined ? "Saved" : ""}
                  value={smtpPassword ?? ""}
                  onChange={(e) => setSmtpPassword(e.target.value)}
                />
                {form.smtpHasPassword && smtpPassword === undefined && (
                  <Button type="button" variant="ghost" onClick={() => setSmtpPassword("")}>
                    Remove
                  </Button>
                )}
              </div>
              {smtpPassword === "" && form.smtpHasPassword && <FieldDescription>The saved password is removed when you save.</FieldDescription>}
            </Field>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="smtpFrom">Send from</FieldLabel>
              <Input id="smtpFrom" type="email" placeholder="files@example.com" value={form.smtpFrom} onChange={(e) => set("smtpFrom", e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="smtpFromName">Sender name</FieldLabel>
              <Input id="smtpFromName" placeholder={form.appName} maxLength={80} value={form.smtpFromName} onChange={(e) => set("smtpFromName", e.target.value)} />
            </Field>
          </div>
          <Field>
            <FieldLabel htmlFor="testTo">Send a test email</FieldLabel>
            <div className="flex gap-2">
              <Input id="testTo" type="email" placeholder="you@example.com" value={testTo} onChange={(e) => setTestTo(e.target.value)} />
              <Button type="button" variant="outline" onClick={sendTest} disabled={testing || !testTo.trim() || !data?.mailReady}>
                {testing ? <Spinner /> : <MailIcon />} Send
              </Button>
            </div>
            <FieldDescription>Save your changes first; the test uses the saved settings.</FieldDescription>
          </Field>
        </SettingsCard>

        <SettingsCard title="Health and backups" description="Coffer checks every few minutes for offline drives, low space, missed backups, a stopped media worker and failing search.">
          <Toggle
            id="healthEmail"
            label="Email admins about problems"
            description="Admins are always told in the app. Email goes to admins who added an address, once a day at most per problem, and again when it's fixed."
            checked={form.healthEmail}
            onChange={(v) => set("healthEmail", v)}
          />
          <Field>
            <FieldLabel>Copy Coffer's database and key into backups</FieldLabel>
            <Select value={form.selfBackup} onValueChange={(v) => set("selfBackup", v as Settings["selfBackup"])}>
              <SelectTrigger className="w-full sm:w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">Local drives and encrypted buckets</SelectItem>
                <SelectItem value="on">Every backup</SelectItem>
                <SelectItem value="off">Never</SelectItem>
              </SelectContent>
            </Select>
            <FieldDescription>
              Lets you restore Coffer itself, with its users, links and settings. The key unlocks stored passwords and drive keys, so only keep it where
              nobody else can read it.
            </FieldDescription>
          </Field>
        </SettingsCard>

        <SettingsCard title="Share pages" description="What recipients see at the bottom of every link page.">
          <Field>
            <FieldLabel htmlFor="publicUrl">Public address</FieldLabel>
            <Input id="publicUrl" readOnly value={form.publicUrl || "Not set"} className="text-muted-foreground" />
            <FieldDescription>
              Set with COFFER_PUBLIC_URL, usually the hostname of your Cloudflare Tunnel. Links use the address you browse from until it is set.
            </FieldDescription>
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="shareFooter">Footer text</FieldLabel>
              <Input id="shareFooter" maxLength={300} placeholder="Studio name · City" value={form.shareFooter} onChange={(e) => set("shareFooter", e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="shareWebsite">Website</FieldLabel>
              <Input id="shareWebsite" type="url" placeholder="https://example.com" value={form.shareWebsite} onChange={(e) => set("shareWebsite", e.target.value)} />
            </Field>
          </div>
          <Toggle id="powered" label="Hide “Powered by Coffer”" checked={form.shareHidePowered} onChange={(v) => set("shareHidePowered", v)} />
        </SettingsCard>

        <SettingsCard title="Previews" description="The media worker makes browser-friendly copies of videos (ProRes, HEVC, MXF) and HEIC or RAW photos.">
          <Toggle
            id="mediaAuto"
            label="Prepare previews for new uploads"
            description="Otherwise previews are made the first time someone opens a file."
            checked={form.mediaAuto}
            onChange={(v) => set("mediaAuto", v)}
          />
          <Field>
            <FieldLabel>Preview video size</FieldLabel>
            <Select value={String(form.proxyHeight)} onValueChange={(v) => set("proxyHeight", Number(v) as Settings["proxyHeight"])}>
              <SelectTrigger className="w-full sm:w-56">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="720">720p (smaller, faster)</SelectItem>
                <SelectItem value="1080">1080p</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel htmlFor="mediaCacheGB">Keep previews up to (GB)</FieldLabel>
            <Input id="mediaCacheGB" type="number" min={0} className="sm:w-56" value={form.mediaCacheGB} onChange={num("mediaCacheGB")} />
            <FieldDescription>The least recently opened go first. Previews nobody opened for 60 days are always removed. 0 = no limit</FieldDescription>
          </Field>
        </SettingsCard>

        <SettingsCard title="Files & indexing" description="The index powers instant search and folder sizes.">
          <Toggle id="dot" label="Hide hidden and system files" description="Dot-files, .DS_Store, Thumbs.db and similar." checked={form.hideDotfiles} onChange={(v) => set("hideDotfiles", v)} />
          <div className="grid gap-4 sm:grid-cols-3">
            <Field>
              <FieldLabel htmlFor="trash">Empty trash after (days)</FieldLabel>
              <Input id="trash" type="number" min={0} value={form.trashDays} onChange={num("trashDays")} />
              <FieldDescription>0 = keep until emptied by hand</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="scan">Rescan local drives every (min)</FieldLabel>
              <Input id="scan" type="number" min={0} value={form.scanMinutes} onChange={num("scanMinutes")} />
              <FieldDescription>Picks up changes made over SMB. 0 = never automatically</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="scans3">Rescan buckets every (min)</FieldLabel>
              <Input id="scans3" type="number" min={0} value={form.scanMinutesS3} onChange={num("scanMinutesS3")} />
              <FieldDescription>Bucket listings cost API calls. 0 = never automatically</FieldDescription>
            </Field>
          </div>
        </SettingsCard>

        <SettingsCard title="Connections & updates">
          <Toggle
            id="webdav"
            label="WebDAV for desktop apps"
            description="Lets people mount their files in Finder, Explorer and Linux file managers with app passwords."
            checked={form.webdav}
            onChange={(v) => set("webdav", v)}
          />
          <Toggle
            id="updates"
            label="Check for updates"
            description="Asks GitHub for new releases twice a day. Only the version number is sent."
            checked={form.updateCheck}
            onChange={(v) => set("updateCheck", v)}
          />
        </SettingsCard>
        <button type="submit" hidden />
      </form>
    </AdminPage>
  )
}
