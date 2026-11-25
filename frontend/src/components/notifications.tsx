import { useQuery, useQueryClient } from "@tanstack/react-query"
import { BellIcon, CheckCheckIcon, DownloadCloudIcon, HeartPulseIcon, LinkIcon, ShieldIcon, UsersIcon } from "lucide-react"
import { useNavigate } from "react-router"

import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { api, type Notification } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

export function useNotifications() {
  return useQuery({ queryKey: ["notifications"], queryFn: api.notifications, refetchInterval: 30_000, staleTime: 15_000 })
}

const icons: Record<string, typeof BellIcon> = { share: LinkIcon, shared: UsersIcon, update: DownloadCloudIcon, security: ShieldIcon, health: HeartPulseIcon }

export function NotificationItem({ n, onClick }: { n: Notification; onClick?: () => void }) {
  const Icon = icons[n.kind] ?? BellIcon
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn("flex w-full gap-3 rounded-lg p-2.5 text-left hover:bg-muted", !n.readAt && "bg-brand/5")}
    >
      <div className="flex size-8 shrink-0 items-center justify-center rounded-full bg-secondary">
        <Icon className="size-4" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{n.title}</div>
        {n.body && <div className="truncate text-xs text-muted-foreground">{n.body}</div>}
        <div className="mt-0.5 text-xs text-muted-foreground">{relativeTime(n.createdAt)}</div>
      </div>
      {!n.readAt && <span className="mt-1.5 size-2 shrink-0 rounded-full bg-brand" />}
    </button>
  )
}

export function NotificationsBell({
  side,
  align = "end",
  className,
}: {
  side?: "top" | "right" | "bottom" | "left"
  align?: "start" | "center" | "end"
  className?: string
}) {
  const { data } = useNotifications()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const unread = data?.unread ?? 0

  const open = async (n: Notification) => {
    if (!n.readAt) await api.markRead([n.id]).catch(() => {})
    qc.invalidateQueries({ queryKey: ["notifications"] })
    if (n.link) navigate(n.link)
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="icon" className={cn("relative text-muted-foreground hover:text-foreground", className)} aria-label={`Notifications${unread ? ` (${unread} unread)` : ""}`}>
          <BellIcon className="size-4.5" />
          {unread > 0 && <span className="absolute top-1.5 right-1.5 size-2 rounded-full bg-brand ring-2 ring-background" />}
        </Button>
      </PopoverTrigger>
      <PopoverContent side={side} align={align} className="w-[min(24rem,calc(100vw-2rem))] p-0">
        <div className="flex items-center justify-between border-b px-3 py-2.5">
          <span className="text-sm font-semibold">Notifications</span>
          {unread > 0 && (
            <Button
              variant="ghost"
              size="xs"
              onClick={async () => {
                await api.markRead()
                qc.invalidateQueries({ queryKey: ["notifications"] })
              }}
            >
              <CheckCheckIcon /> Mark all read
            </Button>
          )}
        </div>
        <div className="max-h-96 overflow-y-auto p-1.5">
          {data?.items.length ? (
            data.items.slice(0, 20).map((n) => <NotificationItem key={n.id} n={n} onClick={() => open(n)} />)
          ) : (
            <p className="p-6 text-center text-sm text-muted-foreground">You're all caught up.</p>
          )}
        </div>
        <div className="border-t p-1.5">
          <Button variant="ghost" size="sm" className="w-full" onClick={() => navigate("/notifications")}>
            View all
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}
