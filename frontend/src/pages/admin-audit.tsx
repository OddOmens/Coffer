import { useState } from "react"
import { useInfiniteQuery } from "@tanstack/react-query"
import { ScrollTextIcon } from "lucide-react"

import { AdminPage } from "@/components/admin-page"
import { EmptyState, Spinner } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { formatDate } from "@/lib/format"

const groups: [string, string[]][] = [
  ["Sign-in", ["login", "login.failed", "password.change", "2fa.enable", "2fa.disable", "2fa.recovery_used", "2fa.recovery_regenerated"]],
  ["Files", ["upload", "edit", "file.create", "folder.create", "rename", "move", "copy", "delete", "delete.permanent", "restore", "trash.purge", "trash.empty"]],
  ["Links", ["share.create", "share.update", "share.revoke", "share.download", "share.download_zip", "share.upload", "share.password_failed"]],
  ["People & access", ["grant.create", "grant.delete", "place.create", "place.update", "user.create", "user.update", "user.delete"]],
  ["Desktop apps", ["apptoken.create", "apptoken.delete", "webdav.denied"]],
  ["Administration", ["drive.create", "drive.update", "drive.delete", "settings.update", "settings.logo"]],
]

function tone(action: string): "destructive" | "outline" | "secondary" {
  if (action.includes("fail") || action.includes("denied") || action.startsWith("delete")) return "destructive"
  if (action.startsWith("share.")) return "outline"
  return "secondary"
}

export default function AuditPage() {
  const [action, setAction] = useState("all")
  const q = useInfiniteQuery({
    queryKey: ["audit", action],
    queryFn: ({ pageParam }) => api.audit(pageParam || undefined, action === "all" ? undefined : action),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.length === 100 ? last[last.length - 1].id : undefined),
  })
  const rows = q.data?.pages.flat() ?? []

  return (
    <AdminPage
      description="Sign-ins, changes and every public link access. Kept for one year."
      actions={
        <Select value={action} onValueChange={setAction}>
          <SelectTrigger className="w-56">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All events</SelectItem>
            {groups.map(([label, actions]) => (
              <SelectGroup key={label}>
                <SelectLabel>{label}</SelectLabel>
                {actions.map((a) => (
                  <SelectItem key={a} value={a}>
                    {a}
                  </SelectItem>
                ))}
              </SelectGroup>
            ))}
          </SelectContent>
        </Select>
      }
    >
      {q.isLoading ? (
        <Spinner />
      ) : !rows.length ? (
        <EmptyState icon={<ScrollTextIcon />} title="No events" description={action === "all" ? undefined : `Nothing recorded for ${action}.`} />
      ) : (
        <div className="overflow-hidden rounded-xl border bg-card">
          <Table>
            <TableHeader className="bg-muted/40">
              <TableRow>
                <TableHead className="w-44 pl-4">Time</TableHead>
                <TableHead>Event</TableHead>
                <TableHead>Who</TableHead>
                <TableHead className="hidden md:table-cell">Item</TableHead>
                <TableHead className="hidden lg:table-cell">IP</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="pl-4 text-muted-foreground tabular-nums">{formatDate(e.ts)}</TableCell>
                  <TableCell>
                    <Badge variant={tone(e.action)}>{e.action}</Badge>
                  </TableCell>
                  <TableCell>{e.username || (e.shareId ? <span className="text-muted-foreground">link {e.shareId}</span> : "—")}</TableCell>
                  <TableCell className="hidden max-w-96 md:table-cell">
                    <div className="truncate font-mono text-xs">{e.path}</div>
                    {e.detail && <div className="truncate text-xs text-muted-foreground">{e.detail}</div>}
                  </TableCell>
                  <TableCell className="hidden font-mono text-xs text-muted-foreground lg:table-cell">{e.ip}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {q.hasNextPage && (
        <div className="mt-4">
          <Button variant="outline" onClick={() => q.fetchNextPage()} disabled={q.isFetchingNextPage}>
            {q.isFetchingNextPage && <Spinner />} Load more
          </Button>
        </div>
      )}
    </AdminPage>
  )
}
