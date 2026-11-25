import type { ReactNode } from "react"
import { useLocation, useNavigate } from "react-router"

import { Page } from "@/components/page"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"

const ADMIN_SECTIONS = [
  { to: "/admin/users", label: "Users" },
  { to: "/admin/drives", label: "Drives" },
  { to: "/admin/monitor", label: "Monitoring" },
  { to: "/admin/backups", label: "Backups" },
  { to: "/admin/settings", label: "App settings" },
  { to: "/admin/audit", label: "Audit log" },
] as const

/**
 * Frame for the admin pages: one "Admin settings" page with a tab per
 * section, laid out like User settings. Each tab keeps its own address.
 */
export function AdminPage({ description, actions, children }: { description?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const here = ADMIN_SECTIONS.find((s) => pathname === s.to || pathname.startsWith(s.to + "/"))?.to ?? ""

  return (
    <Page title="Admin settings">
      <Tabs value={here} onValueChange={(v) => navigate(v)} activationMode="manual" className="mb-6">
        <div className="-mx-4 overflow-x-auto px-4 md:mx-0 md:px-0">
          <TabsList variant="chips">
            {ADMIN_SECTIONS.map((s) => (
              <TabsTrigger key={s.to} value={s.to}>
                {s.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
      </Tabs>
      {(description || actions) && (
        <div className="mb-5 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
          <p className="max-w-3xl flex-1 text-sm text-muted-foreground">{description}</p>
          <div className="flex shrink-0 items-center gap-2">{actions}</div>
        </div>
      )}
      {children}
    </Page>
  )
}
