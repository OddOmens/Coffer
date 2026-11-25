import { Fragment, type ReactNode } from "react"

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"

export function PathBreadcrumbs({
  path,
  rootLabel,
  onNavigate,
  rootIcon,
}: {
  path: string
  rootLabel: string
  rootIcon?: ReactNode
  onNavigate: (path: string) => void
}) {
  const parts = path.split("/").filter(Boolean)
  const crumbs = [{ label: rootLabel, path: "/" }, ...parts.map((p, i) => ({ label: p, path: "/" + parts.slice(0, i + 1).join("/") }))]
  // Long paths collapse their middle.
  const shown = crumbs.length > 4 ? [crumbs[0], { label: "…", path: crumbs[crumbs.length - 3].path }, ...crumbs.slice(-2)] : crumbs

  return (
    <Breadcrumb className="min-w-0">
      <BreadcrumbList className="flex-nowrap">
        {shown.map((c, i) => (
          <Fragment key={c.path + i}>
            {i > 0 && <BreadcrumbSeparator />}
            <BreadcrumbItem className="min-w-0">
              {i === shown.length - 1 ? (
                <BreadcrumbPage className="flex items-center gap-1.5 truncate font-medium">
                  {i === 0 && rootIcon}
                  {c.label}
                </BreadcrumbPage>
              ) : (
                <BreadcrumbLink asChild>
                  <button type="button" className="flex max-w-40 items-center gap-1.5 truncate" onClick={() => onNavigate(c.path)}>
                    {i === 0 && rootIcon}
                    {c.label}
                  </button>
                </BreadcrumbLink>
              )}
            </BreadcrumbItem>
          </Fragment>
        ))}
      </BreadcrumbList>
    </Breadcrumb>
  )
}
