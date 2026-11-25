import { useQueryClient } from "@tanstack/react-query"
import { BellIcon, CheckCheckIcon } from "lucide-react"
import { useNavigate } from "react-router"

import { NotificationItem, useNotifications } from "@/components/notifications"
import { Page } from "@/components/page"
import { EmptyState, Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { api, type Notification } from "@/lib/api"

export default function NotificationsPage() {
  const { data, isLoading } = useNotifications()
  const qc = useQueryClient()
  const navigate = useNavigate()

  const refresh = () => qc.invalidateQueries({ queryKey: ["notifications"] })
  const open = async (n: Notification) => {
    if (!n.readAt) await api.markRead([n.id]).catch(() => {})
    refresh()
    if (n.link) navigate(n.link)
  }

  return (
    <Page
      title="Notifications"
      description="Link activity, folders shared with you, security events and updates."
      actions={
        (data?.unread ?? 0) > 0 && (
          <Button
            variant="outline"
            onClick={async () => {
              await api.markRead()
              refresh()
            }}
          >
            <CheckCheckIcon /> Mark all read
          </Button>
        )
      }
    >
      {isLoading ? (
        <Spinner />
      ) : !data?.items.length ? (
        <EmptyState icon={<BellIcon />} title="You're all caught up" description="Notifications about your links and shared folders appear here." />
      ) : (
        <div className="space-y-1 rounded-xl border bg-card p-2">
          {data.items.map((n) => (
            <NotificationItem key={n.id} n={n} onClick={() => open(n)} />
          ))}
        </div>
      )}
    </Page>
  )
}
