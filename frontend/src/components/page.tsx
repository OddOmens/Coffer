import type { ReactNode, RefObject } from "react"
import { Fragment, useEffect, useRef, useState } from "react"
import { ChevronLeftIcon } from "lucide-react"
import { Link } from "react-router"

import { NotificationsBell } from "@/components/notifications"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { cn } from "@/lib/utils"

export type PageParent = { label: ReactNode; to?: string; onClick?: () => void }

/**
 * Standard page frame: title row with global actions, then content. Every
 * view uses the same width, and the title always sits in the same place.
 * Nested views pass `parents` and get a small breadcrumb trail under the
 * title ("Files › Projects › Acme"); an empty list still draws the trail so
 * a drive's root lines up with its folders.
 */
export function Page({
  title,
  parents,
  crumb,
  description,
  actions,
  children,
  className,
}: {
  title: ReactNode
  parents?: PageParent[]
  /** Label for the current page in the trail, when `title` isn't plain text. */
  crumb?: ReactNode
  description?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
}) {
  const bar = useRef<HTMLDivElement>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const compact = useScrolledPast(heading, bar)
  const back = parents?.[parents.length - 1]
  return (
    <div className="flex min-h-full flex-col">
      <div className={cn("mx-auto w-full max-w-[1600px] px-4 pt-0 pb-8 md:px-6 md:pt-7 lg:px-8 lg:pt-8", className)}>
        {/* Phones: a translucent navigation bar under the status bar. The large
            title below scrolls away and a small one fades in here, as on iOS. */}
        <div
          ref={bar}
          className={cn(
            "sticky top-0 z-30 -mx-4 mb-1 border-b border-transparent bg-background/85 pt-[env(safe-area-inset-top)] backdrop-blur-xl backdrop-saturate-150 transition-colors duration-200 md:hidden",
            compact && "border-border",
          )}
        >
          <div className="relative flex h-11 items-center gap-1 px-1.5">
            {back && <BackButton parent={back} />}
            <div className="flex-1" />
            <NotificationsBell />
            <div
              aria-hidden={!compact}
              className={cn(
                "pointer-events-none absolute inset-x-24 top-1/2 -translate-y-1/2 truncate text-center text-[0.9375rem] font-semibold transition-opacity duration-200",
                compact ? "opacity-100" : "opacity-0",
              )}
            >
              {crumb ?? title}
            </div>
          </div>
        </div>
        <div className="mb-6 flex flex-wrap items-start justify-between gap-x-4 gap-y-3 md:flex-nowrap">
          <div className="min-w-0 flex-1">
            <h1 ref={heading} className="truncate text-[1.75rem] leading-tight font-bold tracking-tight md:text-[1.875rem] md:font-medium md:tracking-[-0.025em]">
              {title}
            </h1>
            {parents && <Trail parents={parents} current={crumb ?? title} />}
            {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </div>
        {children}
      </div>
    </div>
  )
}

/** iOS-style back button to the page above this one. */
function BackButton({ parent }: { parent: PageParent }) {
  const cls = "flex min-w-0 max-w-[40vw] items-center rounded-lg py-2 pr-2 text-[0.9375rem] text-brand active:opacity-50"
  const body = (
    <>
      <ChevronLeftIcon className="size-6 shrink-0" />
      <span className="truncate">{parent.label}</span>
    </>
  )
  return parent.to ? (
    <Link to={parent.to} className={cls}>
      {body}
    </Link>
  ) : (
    <button type="button" onClick={parent.onClick} className={cls}>
      {body}
    </button>
  )
}

/** Whether `target` has scrolled up under the sticky `bar`. False while the bar is hidden. */
function useScrolledPast(target: RefObject<HTMLElement | null>, bar: RefObject<HTMLElement | null>) {
  const [past, setPast] = useState(false)
  useEffect(() => {
    const check = () => {
      const t = target.current?.getBoundingClientRect()
      const b = bar.current?.getBoundingClientRect()
      setPast(!!t && !!b && b.height > 0 && t.bottom <= b.bottom)
    }
    check()
    window.addEventListener("scroll", check, { passive: true })
    window.addEventListener("resize", check)
    return () => {
      window.removeEventListener("scroll", check)
      window.removeEventListener("resize", check)
    }
  }, [target, bar])
  return past
}

/** Small breadcrumb trail under a page title. Long trails keep the first
 * and last two parents and fold the middle into "…". */
function Trail({ parents, current }: { parents: PageParent[]; current: ReactNode }) {
  const shown: PageParent[] =
    parents.length > 3 ? [parents[0], { ...parents[parents.length - 3], label: "…" }, ...parents.slice(-2)] : parents
  return (
    <Breadcrumb className="mt-1 min-w-0">
      <BreadcrumbList className="flex-nowrap gap-1 text-xs sm:gap-1.5">
        {shown.map((p, i) => (
          <Fragment key={i}>
            <BreadcrumbItem className="min-w-0 shrink">
              <BreadcrumbLink asChild>
                {p.to ? (
                  <Link to={p.to} className="max-w-40 truncate">
                    {p.label}
                  </Link>
                ) : (
                  <button type="button" onClick={p.onClick} className="max-w-40 truncate">
                    {p.label}
                  </button>
                )}
              </BreadcrumbLink>
            </BreadcrumbItem>
            <BreadcrumbSeparator className="[&>svg]:size-3" />
          </Fragment>
        ))}
        <BreadcrumbItem className="min-w-0">
          <BreadcrumbPage className="truncate">{current}</BreadcrumbPage>
        </BreadcrumbItem>
      </BreadcrumbList>
    </Breadcrumb>
  )
}

export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn("mb-6", className)}>
      <div className="mb-2.5 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="text-[0.9375rem] font-medium">{title}</h2>
          {description && <p className="text-sm text-muted-foreground">{description}</p>}
        </div>
        {actions}
      </div>
      {children}
    </section>
  )
}
