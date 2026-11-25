import { useEffect, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { FolderIcon, MoreHorizontalIcon, PencilIcon, PlusIcon, ShieldCheckIcon, Trash2Icon, UserPlusIcon } from "lucide-react"
import { toast } from "sonner"

import { useUser } from "@/components/app-layout"
import { AdminPage } from "@/components/admin-page"
import { Spinner } from "@/components/states"
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
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldSeparator } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api, type Grant, type Me } from "@/lib/api"
import { formatBytes, relativeTime } from "@/lib/format"

type UserRow = Awaited<ReturnType<typeof api.users>>[number]
type UserPlace = Awaited<ReturnType<typeof api.userPlaces>>[number]

export default function UsersPage() {
  const me = useUser()
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ["users"], queryFn: api.users })
  const [editing, setEditing] = useState<UserRow | "new" | null>(null)
  const [deleting, setDeleting] = useState<UserRow | null>(null)

  const remove = async () => {
    if (!deleting) return
    try {
      await api.deleteUser(deleting.id)
      toast.success(`${deleting.username} deleted`)
      qc.invalidateQueries({ queryKey: ["users"] })
    } catch (e) {
      toast.error((e as Error).message)
    }
    setDeleting(null)
  }

  return (
    <AdminPage
      description="People who can sign in. Each user only sees the folders you give them."
      actions={
        <Button onClick={() => setEditing("new")}>
          <UserPlusIcon /> Add user
        </Button>
      }
    >
      {isLoading ? (
        <Skeleton className="h-64 w-full rounded-xl" />
      ) : (
        <div className="overflow-hidden rounded-xl border bg-card">
          <Table>
            <TableHeader className="bg-muted/40">
              <TableRow>
                <TableHead className="pl-4">User</TableHead>
                <TableHead className="hidden md:table-cell">Folders</TableHead>
                <TableHead className="hidden lg:table-cell">Updated</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.map((u) => (
                <TableRow key={u.id} className={u.disabled ? "opacity-60" : undefined}>
                  <TableCell className="py-3 pl-4">
                    <div className="flex items-center gap-3">
                      <Avatar className="size-10">
                        <AvatarFallback className="bg-secondary text-sm font-semibold">{u.username.slice(0, 2).toUpperCase()}</AvatarFallback>
                      </Avatar>
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="font-medium">{u.username}</span>
                          {u.id === me.id && <span className="text-xs text-muted-foreground">(you)</span>}
                          {u.totpEnabled && <ShieldCheckIcon className="size-4 text-emerald-600 dark:text-emerald-400" aria-label="Two-factor on" />}
                        </div>
                        {u.email && <div className="truncate text-xs text-muted-foreground">{u.email}</div>}
                        <div className="flex flex-wrap gap-1 pt-0.5">
                          <Badge variant={u.role === "admin" ? "default" : "secondary"} className="capitalize">
                            {u.role}
                          </Badge>
                          {u.disabled && <Badge variant="destructive">Disabled</Badge>}
                          {u.quotaBytes > 0 && (
                            <Badge variant="outline" className={u.quotaUsed > u.quotaBytes * 0.9 ? "border-destructive/50 text-destructive" : undefined}>
                              {formatBytes(u.quotaUsed)} of {formatBytes(u.quotaBytes)}
                            </Badge>
                          )}
                        </div>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="hidden max-w-72 md:table-cell">
                    {u.role === "admin" ? (
                      <span className="text-sm text-muted-foreground">All drives</span>
                    ) : u.places.length ? (
                      <div className="flex flex-wrap gap-1">
                        {u.places.slice(0, 3).map((p) => (
                          <Badge key={p.id} variant="outline" className="max-w-40 truncate">
                            <FolderIcon /> {p.name}
                            {p.grantedById ? " (shared)" : ""}
                          </Badge>
                        ))}
                        {u.places.length > 3 && <Badge variant="outline">+{u.places.length - 3}</Badge>}
                      </div>
                    ) : (
                      <span className="text-sm text-destructive">No folders</span>
                    )}
                  </TableCell>
                  <TableCell className="hidden text-muted-foreground lg:table-cell">{relativeTime(u.updatedAt)}</TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${u.username}`}>
                          <MoreHorizontalIcon />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onSelect={() => setEditing(u)}>
                          <PencilIcon /> Edit
                        </DropdownMenuItem>
                        {u.id !== me.id && (
                          <>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem variant="destructive" onSelect={() => setDeleting(u)}>
                              <Trash2Icon /> Delete
                            </DropdownMenuItem>
                          </>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <UserDialog user={editing} onClose={() => setEditing(null)} />
      <AlertDialog open={!!deleting} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {deleting?.username}?</AlertDialogTitle>
            <AlertDialogDescription>Their share links and folder access are removed. Their files stay where they are.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={remove}>
              Delete user
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </AdminPage>
  )
}

type Perms = { canWrite: boolean; canDelete: boolean; canShare: boolean }

const GB = 1024 ** 3
const MB = 1024 ** 2

/** Splits a byte limit into a number and unit for the form; 0 = unlimited. */
function quotaFields(bytes: number): { quota: string; quotaUnit: "GB" | "MB" } {
  if (!bytes) return { quota: "", quotaUnit: "GB" }
  if (bytes % GB === 0) return { quota: String(bytes / GB), quotaUnit: "GB" }
  return { quota: String(Math.round(bytes / MB)), quotaUnit: "MB" }
}

function quotaMB(quota: string, unit: "GB" | "MB") {
  const n = Math.max(0, Number(quota) || 0)
  return Math.round(unit === "GB" ? n * 1024 : n)
}

function PermSwitches({ value, onChange }: { value: Perms; onChange: (p: Perms) => void }) {
  const item = (key: keyof Perms, label: string) => (
    <label className="flex items-center gap-2 text-sm">
      <Switch checked={value[key]} onCheckedChange={(v) => onChange({ ...value, [key]: v })} />
      {label}
    </label>
  )
  return (
    <div className="flex flex-wrap gap-x-5 gap-y-2">
      {item("canWrite", "Upload & edit")}
      {item("canDelete", "Delete")}
      {item("canShare", "Share")}
    </div>
  )
}

/** The main drive when it is online, otherwise the first online one. */
function defaultDrive(list?: { id: string; online: boolean }[]) {
  return list?.find((d) => d.online && d.id === "files")?.id ?? list?.find((d) => d.online)?.id
}

function useDrives() {
  return useQuery({ queryKey: ["drives"], queryFn: api.drives })
}

type FolderForm = { driveId: string; path: string; name: string; create: boolean } & Perms

function FolderFields({ value, onChange, showCreate = true }: { value: FolderForm; onChange: (v: FolderForm) => void; showCreate?: boolean }) {
  const drives = useDrives()
  return (
    <div className="grid gap-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field>
          <FieldLabel>Drive</FieldLabel>
          <Select value={value.driveId} onValueChange={(v) => onChange({ ...value, driveId: v })}>
            <SelectTrigger className="w-full">
              <SelectValue placeholder="Pick a drive" />
            </SelectTrigger>
            <SelectContent>
              {drives.data?.drives.map((d) => (
                <SelectItem key={d.id} value={d.id} disabled={!d.online}>
                  {d.name}
                  {!d.online && " (offline)"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel>Display name</FieldLabel>
          <Input value={value.name} maxLength={64} onChange={(e) => onChange({ ...value, name: e.target.value })} />
        </Field>
      </div>
      <Field>
        <FieldLabel>Folder on the drive</FieldLabel>
        <Input className="font-mono" value={value.path} onChange={(e) => onChange({ ...value, path: e.target.value })} />
        <FieldDescription>The user sees only this folder and what is inside it. “/” is the whole drive.</FieldDescription>
      </Field>
      {showCreate && (
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={value.create} onCheckedChange={(v) => onChange({ ...value, create: v })} />
          Create the folder if it does not exist
        </label>
      )}
      <PermSwitches value={value} onChange={(p) => onChange({ ...value, ...p })} />
    </div>
  )
}

function UserDialog({ user, onClose }: { user: UserRow | "new" | null; onClose: () => void }) {
  const qc = useQueryClient()
  const drives = useDrives()
  const isNew = user === "new"
  const existing = user && user !== "new" ? user : null
  const blank = { username: "", password: "", role: "user", disabled: false, resetTotp: false, email: "", quota: "", quotaUnit: "GB" as "GB" | "MB" }
  const [form, setForm] = useState(blank)
  const [home, setHome] = useState<FolderForm>({ driveId: "", path: "/Users/", name: "My files", create: true, canWrite: true, canDelete: true, canShare: true })
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!user) return
    setForm(
      existing
        ? {
            username: existing.username,
            password: "",
            role: existing.role,
            disabled: existing.disabled,
            resetTotp: false,
            email: existing.email ?? "",
            ...quotaFields(existing.quotaBytes ?? 0),
          }
        : blank,
    )
    setHome((h) => ({ ...h, driveId: h.driveId || defaultDrive(drives.data?.drives) || "", path: "/Users/" }))
  }, [user]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!home.driveId && drives.data) setHome((h) => ({ ...h, driveId: defaultDrive(drives.data.drives) ?? "" }))
  }, [drives.data, home.driveId])

  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    setBusy(true)
    const body: Record<string, unknown> = {
      username: form.username,
      role: form.role,
      email: form.email.trim(),
      quotaMB: form.role === "admin" ? 0 : quotaMB(form.quota, form.quotaUnit),
    }
    if (form.password) body.password = form.password
    try {
      if (existing) {
        body.disabled = form.disabled
        if (form.resetTotp) body.resetTotp = true
        await api.updateUser(existing.id, body)
      } else {
        if (form.role !== "admin") {
          if (!home.driveId) throw new Error("Pick a drive for the home folder")
          body.home = home
        }
        await api.createUser(body)
      }
      toast.success(existing ? "User updated" : "User created")
      qc.invalidateQueries({ queryKey: ["users"] })
      onClose()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!user} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-xl">
        <form onSubmit={submit} className="grid gap-5">
          <DialogHeader>
            <DialogTitle>{isNew ? "Add user" : `Edit ${existing?.username}`}</DialogTitle>
            <DialogDescription>Changing the password, role or status signs the user out everywhere. A new password also revokes their app passwords.</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="u-name">Username</FieldLabel>
                <Input
                  id="u-name"
                  required
                  autoComplete="off"
                  value={form.username}
                  onChange={(e) => {
                    const v = e.target.value
                    setForm({ ...form, username: v })
                    if (isNew && (home.path === "/Users/" || home.path.startsWith("/Users/"))) setHome({ ...home, path: `/Users/${v}` })
                  }}
                />
              </Field>
              <Field>
                <FieldLabel>Role</FieldLabel>
                <Select value={form.role} onValueChange={(v) => setForm({ ...form, role: v })}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="user">User</SelectItem>
                    <SelectItem value="admin">Administrator (all drives)</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="u-email">Email</FieldLabel>
              <Input
                id="u-email"
                type="email"
                autoComplete="off"
                placeholder="Optional, for notifications"
                value={form.email}
                onChange={(e) => setForm({ ...form, email: e.target.value })}
              />
            </Field>
            {form.role !== "admin" && (
              <Field>
                <FieldLabel htmlFor="u-quota">Storage limit</FieldLabel>
                <div className="flex gap-2">
                  <Input
                    id="u-quota"
                    type="number"
                    min={0}
                    step="any"
                    placeholder="No limit"
                    value={form.quota}
                    onChange={(e) => setForm({ ...form, quota: e.target.value })}
                  />
                  <Select value={form.quotaUnit} onValueChange={(v) => setForm({ ...form, quotaUnit: v as "GB" | "MB" })}>
                    <SelectTrigger className="w-24">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="GB">GB</SelectItem>
                      <SelectItem value="MB">MB</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <FieldDescription>
                  Covers their home folders, including uploads from people they share with.
                  {existing && existing.quotaBytes > 0 && ` Using ${formatBytes(existing.quotaUsed)} now.`}
                </FieldDescription>
              </Field>
            )}
            <Field>
              <FieldLabel htmlFor="u-pw">{isNew ? "Password" : "New password"}</FieldLabel>
              <Input
                id="u-pw"
                type="password"
                autoComplete="new-password"
                required={isNew}
                minLength={10}
                placeholder={isNew ? "At least 10 characters" : "Leave blank to keep"}
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
              />
            </Field>
            {existing && (
              <>
                <Field orientation="horizontal">
                  <div className="flex-1">
                    <FieldLabel htmlFor="u-dis">Disabled</FieldLabel>
                    <FieldDescription>Blocks sign-in and pauses this user's share links.</FieldDescription>
                  </div>
                  <Switch id="u-dis" checked={form.disabled} onCheckedChange={(v) => setForm({ ...form, disabled: v })} />
                </Field>
                {existing.totpEnabled && (
                  <Field orientation="horizontal">
                    <div className="flex-1">
                      <FieldLabel htmlFor="u-2fa">Reset two-factor</FieldLabel>
                      <FieldDescription>Use if they lost their device.</FieldDescription>
                    </div>
                    <Switch id="u-2fa" checked={form.resetTotp} onCheckedChange={(v) => setForm({ ...form, resetTotp: v })} />
                  </Field>
                )}
              </>
            )}
            {isNew && form.role !== "admin" && (
              <>
                <FieldSeparator />
                <div>
                  <div className="mb-3 text-sm font-semibold">Home folder</div>
                  <FolderFields value={home} onChange={setHome} />
                </div>
              </>
            )}
          </FieldGroup>
          {existing && existing.role !== "admin" && (
            <>
              <FieldSeparator />
              <UserFolders user={existing} />
            </>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner />}
              {isNew ? "Create user" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function UserFolders({ user }: { user: Me }) {
  const qc = useQueryClient()
  const drives = useDrives()
  const places = useQuery({ queryKey: ["user-places", user.id], queryFn: () => api.userPlaces(user.id) })
  const [edit, setEdit] = useState<{ id: string | null; form: FolderForm } | null>(null)
  const [busy, setBusy] = useState(false)

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["user-places", user.id] })
    qc.invalidateQueries({ queryKey: ["users"] })
  }

  const startAdd = () =>
    setEdit({
      id: null,
      form: { driveId: defaultDrive(drives.data?.drives) ?? "", path: `/Users/${user.username}`, name: "Folder", create: true, canWrite: true, canDelete: true, canShare: true },
    })

  const startEdit = (p: UserPlace) =>
    setEdit({ id: p.id, form: { driveId: p.driveId, path: p.path, name: p.name, create: false, canWrite: p.canWrite, canDelete: p.canDelete, canShare: p.canShare } })

  const save = async () => {
    if (!edit) return
    setBusy(true)
    try {
      if (edit.id) await api.updateUserPlace(edit.id, edit.form)
      else await api.addUserPlace(user.id, edit.form)
      setEdit(null)
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async (p: Grant) => {
    try {
      await api.deletePlace(p.id)
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  return (
    <div>
      <div className="mb-3 flex items-center justify-between">
        <div className="text-sm font-semibold">Folders</div>
        {!edit && (
          <Button type="button" variant="outline" size="sm" onClick={startAdd}>
            <PlusIcon /> Add folder
          </Button>
        )}
      </div>
      {edit && (
        <div className="mb-3 rounded-xl border bg-muted/30 p-3">
          <FolderFields value={edit.form} onChange={(f) => setEdit({ ...edit, form: f })} showCreate={!edit.id} />
          <div className="mt-3 flex justify-end gap-2">
            <Button type="button" variant="ghost" size="sm" onClick={() => setEdit(null)}>
              Cancel
            </Button>
            <Button type="button" size="sm" disabled={busy} onClick={save}>
              {busy && <Spinner />} {edit.id ? "Save folder" : "Add folder"}
            </Button>
          </div>
        </div>
      )}
      {places.isLoading ? (
        <Spinner />
      ) : !places.data?.length ? (
        <p className="text-sm text-muted-foreground">No folders yet. The user cannot see any files.</p>
      ) : (
        <div className="divide-y rounded-xl border">
          {places.data.map((p) => (
            <div key={p.id} className="flex items-center gap-3 p-3">
              <div className="flex size-9 shrink-0 items-center justify-center rounded-[10px] border bg-secondary/70">
                <FolderIcon className="size-4 text-muted-foreground" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">
                  {p.name}
                  {p.grantedBy && <span className="font-normal text-muted-foreground"> · shared by {p.grantedBy}</span>}
                </div>
                <div className="truncate font-mono text-xs text-muted-foreground">
                  {p.driveName}:{p.path}
                </div>
                <div className="text-xs text-muted-foreground">
                  {["Read", p.canWrite && "Write", p.canDelete && "Delete", p.canShare && "Share"].filter(Boolean).join(" · ")}
                </div>
              </div>
              {!p.grantedBy && (
                <Button type="button" variant="ghost" size="icon-sm" onClick={() => startEdit(p)} aria-label={`Edit ${p.name}`}>
                  <PencilIcon />
                </Button>
              )}
              <Button type="button" variant="ghost" size="icon-sm" onClick={() => remove(p)} aria-label={`Remove ${p.name}`}>
                <Trash2Icon />
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
