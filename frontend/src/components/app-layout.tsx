import { useEffect, useState, type ReactNode } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ChevronRightIcon,
  CloudIcon,
  FolderIcon,
  FolderOpenIcon,
  HardDriveIcon,
  HomeIcon,
  LibraryIcon,
  LinkIcon,
  LockIcon,
  LogOutIcon,
  MenuIcon,
  MonitorIcon,
  MoonIcon,
  SearchIcon,
  ShieldIcon,
  StarIcon,
  TagIcon,
  SunIcon,
  Trash2Icon,
  UserRoundCogIcon,
  UsersIcon,
  UsersRoundIcon,
} from "lucide-react"
import { Link, Navigate, Outlet, useLocation, useNavigate } from "react-router"

import { Brand } from "@/components/brand"
import { CommandSearch, CommandSearchButton, openSearch } from "@/components/command-search"
import { NotificationsBell, useNotifications } from "@/components/notifications"
import { CenteredSpinner } from "@/components/states"
import { UploadPanel } from "@/components/upload-panel"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Sidebar,
  SidebarContent,
  SidebarFold,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  SidebarTrigger,
  sidebarFade,
  sidebarLabel,
  useSidebar,
  useSidebarRail,
} from "@/components/ui/sidebar"
import { api, ApiError, setUnauthenticatedHandler, type Me, type Place } from "@/lib/api"
import { filesUrl } from "@/lib/format"
import { usePrefs, useApplyPrefs } from "@/lib/prefs"
import { cn } from "@/lib/utils"

export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: api.me, staleTime: 60_000, retry: false })
}

/** The signed-in user. Only use below <RequireAuth>. */
export function useUser(): Me {
  return useMe().data!
}

export function usePlaces() {
  return useQuery({ queryKey: ["places"], queryFn: api.places, staleTime: 30_000 })
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const location = useLocation()
  const { data, error, isLoading } = useMe()

  useEffect(() => {
    setUnauthenticatedHandler(() => qc.setQueryData(["me"], null))
  }, [qc])

  if (isLoading) return <CenteredSpinner />
  if (!data || (error instanceof ApiError && error.status === 401)) {
    const next = location.pathname + location.search
    return <Navigate to={`/login${next !== "/home" ? `?next=${encodeURIComponent(next)}` : ""}`} replace />
  }
  if (data.mustEnroll2fa && location.pathname !== "/account") {
    return <Navigate to="/account?tab=security&enroll=1" replace />
  }
  return <>{children}</>
}

export function RequireAdmin({ children }: { children: ReactNode }) {
  const me = useUser()
  if (me.role !== "admin") return <Navigate to="/home" replace />
  return <>{children}</>
}

// Every icon in the sidebar sits in one column, 1.75rem from the card's edge,
// whether it's open or collapsed to icons: rows are inset 0.75rem and padded
// 0.5rem. Labels get a max width in cqw (their row's width, which never
// changes) so they truncate once, not on every frame of the collapse.
const navItem = "h-8 gap-2.5 text-[0.875rem] [&>svg]:size-4 [&>svg]:text-muted-foreground data-[active=true]:[&>svg]:text-foreground"
const navLabel = cn(sidebarLabel, "max-w-[calc(100cqw-2.625rem)]")
const smallItem = "h-7 gap-1.5 px-2 text-[0.8125rem] [&>svg]:text-muted-foreground"
const smallLabel = cn(sidebarLabel, "max-w-[calc(100cqw-2.375rem)]")

type NavItem = { to: string; label: string; icon: typeof HomeIcon; match?: (p: string) => boolean }

function NavList({ items }: { items: NavItem[] }) {
  const { pathname } = useLocation()
  const { isMobile, setOpenMobile } = useSidebar()
  return (
    <SidebarMenu className="gap-px">
      {items.map(({ to, label, icon: Icon, match }) => (
        <SidebarMenuItem key={to}>
          <SidebarMenuButton
            asChild
            isActive={match ? match(pathname) : pathname === to || pathname.startsWith(to + "/")}
            tooltip={label}
            className={navItem}
          >
            <Link to={to} onClick={() => isMobile && setOpenMobile(false)}>
              <Icon />
              <span className={navLabel}>{label}</span>
            </Link>
          </SidebarMenuButton>
        </SidebarMenuItem>
      ))}
    </SidebarMenu>
  )
}

const groupLabel = "h-7 px-2 text-xs font-medium text-muted-foreground"

/** Lazily loaded sub-folders for the sidebar tree. */
function TreeChildren({ place, path, depth }: { place: string; path: string; depth: number }) {
  const { data, isLoading } = useQuery({ queryKey: ["list", place, path], queryFn: () => api.list(place, path), staleTime: 30_000 })
  const dirs = (data?.entries ?? []).filter((e) => e.isDir)
  if (isLoading) return <div className="py-1 pl-8 text-xs text-muted-foreground">Loading…</div>
  if (!dirs.length) return null
  return (
    // Each level's guide line sits under its parent's icon (a place) or chevron (a folder).
    <SidebarMenuSub className={cn("mr-0 gap-px py-px pr-0 pl-1", depth === 0 ? "ml-[0.9375rem]" : "ml-[0.4375rem]")}>
      {dirs.slice(0, 100).map((d) => (
        <TreeNode key={d.path} place={place} path={d.path} name={d.name} depth={depth + 1} />
      ))}
      {dirs.length > 100 && <li className="px-6 py-1 text-xs text-muted-foreground">{dirs.length - 100} more…</li>}
    </SidebarMenuSub>
  )
}

function Chevron({ open, hidden, className }: { open: boolean; hidden?: boolean; className?: string }) {
  if (hidden) return <span className={cn("w-[1.125rem] shrink-0", className)} />
  return (
    <CollapsibleTrigger
      className={cn("flex size-[1.125rem] shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-sidebar-accent hover:text-foreground", className)}
      aria-label={open ? "Collapse" : "Expand"}
    >
      <ChevronRightIcon className={cn("size-3.5 transition-transform duration-150", open && "rotate-90")} />
    </CollapsibleTrigger>
  )
}

function TreeNode({ place, path, name, depth }: { place: string; path: string; name: string; depth: number }) {
  const { pathname } = useLocation()
  const { isMobile, setOpenMobile } = useSidebar()
  const url = decodeURIComponent(filesUrl(place, path))
  const here = decodeURIComponent(pathname)
  const ancestor = here.startsWith(url + "/")
  const [open, setOpen] = useState(ancestor)
  useEffect(() => {
    if (ancestor) setOpen(true)
  }, [ancestor])
  return (
    <SidebarMenuSubItem>
      <Collapsible open={open} onOpenChange={setOpen}>
        <div className="flex items-center gap-0.5">
          <Chevron open={open} hidden={depth >= 6} />
          <SidebarMenuSubButton asChild isActive={here === url} className="h-7 flex-1 translate-x-0 gap-1.5 px-1.5 text-[0.8125rem] data-[active=true]:font-medium">
            <Link to={filesUrl(place, path)} onClick={() => isMobile && setOpenMobile(false)} title={name}>
              {open ? <FolderOpenIcon className="text-muted-foreground!" /> : <FolderIcon className="text-muted-foreground!" />}
              <span className="truncate">{name}</span>
            </Link>
          </SidebarMenuSubButton>
        </div>
        <CollapsibleContent>{open && <TreeChildren place={place} path={path} depth={depth} />}</CollapsibleContent>
      </Collapsible>
    </SidebarMenuSubItem>
  )
}

function PlaceNode({ p }: { p: Place }) {
  const { pathname } = useLocation()
  const { isMobile, setOpenMobile } = useSidebar()
  const rail = useSidebarRail()
  const url = decodeURIComponent(filesUrl(p.id))
  const here = decodeURIComponent(pathname)
  const inside = here.startsWith(url + "/")
  const [open, setOpen] = useState(inside)
  useEffect(() => {
    if (inside) setOpen(true)
  }, [inside])
  const Icon = p.kind === "shared" ? UsersRoundIcon : p.driveKind === "s3" ? CloudIcon : p.kind === "drive" ? HardDriveIcon : FolderOpenIcon
  const where = `${p.driveKind === "s3" ? "Cloud" : "Local"} drive “${p.driveName}”${p.encrypted ? ", encrypted" : ""}`
  const what = p.kind === "drive" ? where : `${p.kind === "shared" ? "Shared folder" : "Folder"} on the ${where.charAt(0).toLowerCase() + where.slice(1)}`
  // What trails the name: lock, offline dot, and the chevron that opens the tree.
  const trailing = (p.encrypted ? 1.125 : 0) + (p.online ? 0 : 0.875) + 1.375
  return (
    <Collapsible asChild open={open} onOpenChange={setOpen}>
      <SidebarMenuItem>
        <SidebarMenuButton
          asChild
          isActive={here === url}
          tooltip={p.online ? p.name : `${p.name} (offline)`}
          className={cn(smallItem, "font-medium data-[active=true]:bg-sidebar-accent")}
        >
          <Link to={filesUrl(p.id)} onClick={() => isMobile && setOpenMobile(false)}>
            <span title={what} className="contents">
              <Icon className={cn(!p.online && "opacity-40")} aria-label={what} />
            </span>
            <span className={cn(sidebarLabel, !p.online && "text-muted-foreground")} style={{ maxWidth: `calc(100cqw - ${2.375 + trailing}rem)` }}>
              {p.name}
            </span>
          </Link>
        </SidebarMenuButton>
        {/* The chevron trails the name, so a place's icon lines up with every other icon. */}
        <div inert={rail} data-sidebar="menu-trailing" className={cn("absolute top-0 right-1 flex h-7 items-center gap-1.5", sidebarFade)}>
          {p.encrypted && <LockIcon className="size-3 shrink-0 opacity-60" aria-label="Encrypted" />}
          {!p.online && <span className="size-1.5 shrink-0 rounded-full bg-amber-500" title="Offline" />}
          <Chevron open={open} hidden={!p.online} />
        </div>
        <SidebarFold>
          <CollapsibleContent>{open && p.online && <TreeChildren place={p.id} path="/" depth={0} />}</CollapsibleContent>
        </SidebarFold>
      </SidebarMenuItem>
    </Collapsible>
  )
}

/** Pinned files and folders, plus a link to tags. */
function Favorites() {
  const { pathname, search } = useLocation()
  const { isMobile, setOpenMobile } = useSidebar()
  const favs = useQuery({ queryKey: ["favorites"], queryFn: api.favorites, staleTime: 60_000 })
  const tags = useQuery({ queryKey: ["tags"], queryFn: api.tags, staleTime: 60_000 })
  const list = favs.data ?? []
  if (!list.length && !tags.data?.length) return null
  const here = decodeURIComponent(pathname + search)
  return (
    <SidebarGroup className="py-1">
      <SidebarGroupLabel className={groupLabel}>Favourites</SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu className="gap-px">
          {list.slice(0, 12).map((f) => {
            const url = f.isDir ? filesUrl(f.place, f.path) : `${filesUrl(f.place, f.path.slice(0, f.path.lastIndexOf("/")) || "/")}?open=${encodeURIComponent(f.name)}`
            return (
              <SidebarMenuItem key={f.place + f.path}>
                <SidebarMenuButton
                  asChild
                  isActive={here === decodeURIComponent(url)}
                  tooltip={f.name}
                  className={smallItem}
                >
                  <Link to={url} onClick={() => isMobile && setOpenMobile(false)} title={`${f.placeName}${f.path}`}>
                    {f.isDir ? <FolderIcon /> : <StarIcon className="fill-amber-400 text-amber-400!" />}
                    <span className={cn(smallLabel, f.missing && "text-muted-foreground line-through")}>{f.name}</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            )
          })}
          {!!tags.data?.length && (
            <SidebarMenuItem>
              <SidebarMenuButton asChild isActive={pathname.startsWith("/tags")} tooltip="Tags" className={smallItem}>
                <Link to="/tags" onClick={() => isMobile && setOpenMobile(false)}>
                  <TagIcon />
                  <span className={smallLabel}>Tags</span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
          )}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

/** PlaceGroup lists the places on one kind of storage. */
function PlaceGroup({ label, places }: { label: string; places: Place[] }) {
  if (!places.length) return null
  return (
    <SidebarGroup className="py-1">
      <SidebarGroupLabel className={groupLabel}>{label}</SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu className="gap-px">
          {places.map((p) => (
            <PlaceNode key={p.id} p={p} />
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

function AppSidebar() {
  const rail = useSidebarRail()
  // Menus open upwards, or beside the rail when it's collapsed to icons.
  const side = rail ? "right" : "top"
  const { data: places } = usePlaces()
  const home = places?.home
  // The home place first, then the rest as the server orders them.
  const sorted = [...(places?.places ?? [])].sort((a, b) => Number(b.id === home) - Number(a.id === home))

  const nav: NavItem[] = [
    { to: "/home", label: "Home", icon: HomeIcon },
    { to: home ? filesUrl(home) : "/files", label: "All files", icon: FolderIcon, match: (p) => p.startsWith("/files") || p.startsWith("/edit") },
    { to: "/shared", label: "Shared with me", icon: UsersIcon },
    { to: "/shares", label: "Shared links", icon: LinkIcon },
    { to: "/collections", label: "Collections", icon: LibraryIcon },
    { to: "/trash", label: "Deleted files", icon: Trash2Icon },
  ]

  return (
    // Inside the safe area: under the status bar and clear of the home indicator on iPad,
    // beside the notch on a phone held sideways.
    <Sidebar
      variant="floating"
      collapsible="icon"
      className="top-[env(safe-area-inset-top)] bottom-[env(safe-area-inset-bottom)] h-auto data-[side=left]:left-[env(safe-area-inset-left)]"
    >
      {/*
        The brand, with the collapse toggle pinned to the card's right edge:
        as the card narrows the toggle rides in with the edge and comes to
        rest in the icon column, where the brand (fading out) was.
      */}
      <SidebarHeader className="flex-row items-center px-3.5 pt-3 pb-1">
        <Link to="/home" inert={rail} className={cn("min-w-0 flex-1 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring", sidebarFade)}>
          <Brand small />
        </Link>
        <SidebarTrigger className="top-3 right-3.5 z-10 hidden shrink-0 bg-sidebar text-muted-foreground md:absolute md:inline-flex" />
      </SidebarHeader>
      <SidebarContent className="px-1 md:pt-1">
        <SidebarGroup className="py-1">
          <SidebarGroupContent>
            <NavList items={nav} />
          </SidebarGroupContent>
        </SidebarGroup>
        <Favorites />
        <PlaceGroup label="Local" places={sorted.filter((p) => p.driveKind !== "s3")} />
        <PlaceGroup label="Cloud" places={sorted.filter((p) => p.driveKind === "s3")} />
      </SidebarContent>
      <SidebarFooter className="gap-0 px-2 py-2">
        {/* Collapsed to icons, search and notifications stack above the account. */}
        <SidebarFold when="rail">
          <div className="flex flex-col gap-1 pb-1 pl-0.5">
            <CommandSearchButton className="size-9 shrink-0" />
            <NotificationsBell side="right" align="end" className="size-9 shrink-0 [&>span]:ring-sidebar" />
          </div>
        </SidebarFold>
        {/* As in Quire: the account, then search and notifications. */}
        <div className="@container flex items-center gap-1">
          <UserMenu side={side} />
          <div inert={rail} className={cn("flex items-center gap-1", sidebarFade)}>
            <CommandSearchButton className="size-9 shrink-0" />
            <NotificationsBell side="top" align="start" className="size-9 shrink-0 [&>span]:ring-sidebar" />
          </div>
        </div>
      </SidebarFooter>
    </Sidebar>
  )
}

function useSignOut() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  return async () => {
    await api.logout().catch(() => {})
    qc.clear()
    navigate("/login")
  }
}

function AccountMenuItems({ onNavigate }: { onNavigate: () => void }) {
  const me = useUser()
  const [prefs, setPrefs] = usePrefs()
  const signOut = useSignOut()
  return (
    <>
      <DropdownMenuItem asChild>
        <Link to="/account" onClick={onNavigate}>
          <UserRoundCogIcon /> User settings
        </Link>
      </DropdownMenuItem>
      {me.role === "admin" && (
        <DropdownMenuItem asChild>
          <Link to="/admin/users" onClick={onNavigate}>
            <ShieldIcon /> Admin settings
          </Link>
        </DropdownMenuItem>
      )}
      <DropdownMenuSub>
        <DropdownMenuSubTrigger>
          {prefs.theme === "dark" ? <MoonIcon /> : prefs.theme === "light" ? <SunIcon /> : <MonitorIcon />} Theme
        </DropdownMenuSubTrigger>
        <DropdownMenuSubContent>
          <DropdownMenuRadioGroup value={prefs.theme} onValueChange={(v) => setPrefs({ theme: v as "light" | "dark" | "system" })}>
            <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="system">System</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuSubContent>
      </DropdownMenuSub>
      <DropdownMenuSeparator />
      <DropdownMenuItem onSelect={() => void signOut()}>
        <LogOutIcon /> Sign out
      </DropdownMenuItem>
    </>
  )
}

/** The account at the foot of the sidebar: avatar and name, as in Quire. */
function UserMenu({ side }: { side: "top" | "right" }) {
  const me = useUser()
  const { isMobile, setOpenMobile } = useSidebar()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label="Account"
          className="flex h-10 w-[calc(100cqw-5rem)] flex-none items-center gap-2.5 overflow-hidden rounded-md px-1.5 text-left outline-none transition-[width] duration-(--sidebar-duration) ease-(--sidebar-ease) hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-sidebar-ring data-[state=open]:bg-sidebar-accent group-data-[collapsible=icon]:w-10"
        >
          <span className="grid size-7 shrink-0 place-items-center rounded-full bg-foreground text-[0.6875rem] font-semibold text-background">{me.username.slice(0, 2).toUpperCase()}</span>
          <span className={cn(sidebarLabel, "max-w-[calc(100cqw-8.125rem)] text-sm font-medium")}>{me.username}</span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side={side} align={side === "top" ? "start" : "end"} className="w-56">
        <DropdownMenuLabel className="font-normal">
          <div className="truncate text-sm font-medium text-foreground">{me.username}</div>
          <div className="text-xs text-muted-foreground capitalize">{me.role}</div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <AccountMenuItems onNavigate={() => isMobile && setOpenMobile(false)} />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

const tabClass = "relative flex flex-col items-center justify-center gap-0.5 pt-1 text-[0.625rem] font-medium tracking-wide text-muted-foreground transition-opacity active:opacity-50 [&_svg]:size-6 [&_svg]:stroke-[1.75]"

/** Phones: an iOS-style tab bar for the main places, with the full sidebar under "More". */
function MobileTabBar() {
  const { pathname } = useLocation()
  const { data: places } = usePlaces()
  const { data: notes } = useNotifications()
  const { openMobile, setOpenMobile } = useSidebar()
  // The editor is a full-screen view of its own.
  if (pathname.startsWith("/edit/")) return null
  const home = places?.home
  const tabs = [
    { to: "/home", label: "Home", icon: HomeIcon, active: pathname === "/home" },
    { to: home ? filesUrl(home) : "/files", label: "Files", icon: FolderIcon, active: pathname.startsWith("/files") },
    { to: "/shared", label: "Shared", icon: UsersIcon, active: pathname.startsWith("/shared") || pathname.startsWith("/shares") },
  ]
  const unread = notes?.unread ?? 0
  const more = openMobile || !tabs.some((t) => t.active)
  return (
    <>
      <div aria-hidden className="h-[calc(var(--tabbar-h)+env(safe-area-inset-bottom))] shrink-0 md:hidden" />
      <nav aria-label="Main" className="fixed inset-x-0 bottom-0 z-40 border-t bg-background/85 pb-[env(safe-area-inset-bottom)] backdrop-blur-xl backdrop-saturate-150 select-none md:hidden">
        <div className="mx-auto grid h-(--tabbar-h) max-w-lg grid-cols-5">
          {tabs.map(({ to, label, icon: Icon, active }) => (
            <Link
              key={label}
              to={to}
              aria-current={active ? "page" : undefined}
              className={cn(tabClass, active && !openMobile && "text-brand")}
              onClick={(e) => {
                setOpenMobile(false)
                // Tapping the tab you're on scrolls back to the top.
                if (decodeURIComponent(pathname) === decodeURIComponent(to)) {
                  e.preventDefault()
                  window.scrollTo({ top: 0, behavior: "smooth" })
                }
              }}
            >
              <Icon />
              {label}
            </Link>
          ))}
          <button
            type="button"
            className={tabClass}
            onClick={() => {
              setOpenMobile(false)
              openSearch()
            }}
          >
            <SearchIcon />
            Search
          </button>
          <button type="button" className={cn(tabClass, more && "text-brand")} onClick={() => setOpenMobile(!openMobile)} aria-expanded={openMobile}>
            <MenuIcon />
            More
            {unread > 0 && (
              <span className="absolute top-1 left-1/2 ml-2 min-w-4 rounded-full bg-destructive px-1 text-center text-[0.625rem] leading-4 font-semibold text-white tabular-nums">
                {unread > 99 ? "99+" : unread}
              </span>
            )}
          </button>
        </div>
      </nav>
    </>
  )
}

export function AppLayout() {
  const [prefs] = usePrefs()
  useApplyPrefs(prefs)
  return (
    // Wider windows: a floating sidebar card, pages on the grey canvas.
    <SidebarProvider className="md:bg-canvas md:pt-[env(safe-area-inset-top)] md:pr-[env(safe-area-inset-right)] md:pb-[env(safe-area-inset-bottom)] md:pl-[env(safe-area-inset-left)]">
      {/* iPad: pages scroll under the status bar, which the phone layout's navigation bar covers. */}
      <div aria-hidden className="pointer-events-none fixed inset-x-0 top-0 z-30 hidden h-[env(safe-area-inset-top)] bg-canvas/85 backdrop-blur-xl md:block" />
      <AppSidebar />
      <SidebarInset className="min-w-0 md:bg-transparent">
        <Outlet />
        <MobileTabBar />
      </SidebarInset>
      <UploadPanel />
      <CommandSearch />
    </SidebarProvider>
  )
}
