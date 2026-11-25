import { useEffect, useState, type ComponentType } from "react"
import { useQuery } from "@tanstack/react-query"
import { Navigate, Route, Routes } from "react-router"

import { AppLayout, RequireAdmin, RequireAuth, usePlaces } from "@/components/app-layout"
import { CenteredSpinner } from "@/components/states"
import { api } from "@/lib/api"
import { applyAccent } from "@/lib/branding"
import { filesUrl } from "@/lib/format"
import NotFoundPage from "@/pages/not-found"
import { loadedScreen, loadScreen, type Screen, type ScreenModule } from "@/routes"

/**
 * A screen whose code loads when it's first shown. One that has already
 * arrived (the app starts fetching the first screen straight away, in
 * lib/boot.ts) renders at once. No React.lazy: see lib/deferred.tsx.
 */
function screen<K extends Screen>(name: K, pick: (m: ScreenModule<K>) => ComponentType = (m) => m.default) {
  return function ScreenRoute() {
    const [, update] = useState(0)
    const m = loadedScreen(name)
    useEffect(() => {
      if (m) return
      let live = true
      // A file that's gone after an update reloads the app (main.tsx).
      loadScreen(name).then(() => live && update((n) => n + 1), () => {})
      return () => {
        live = false
      }
    }, [m])
    if (!m) return <CenteredSpinner />
    const Ready = pick(m)
    return <Ready />
  }
}

const HomePage = screen("home")
const FilesPage = screen("files")
const SharedWithMePage = screen("sharedWithMe")
const SharesPage = screen("shares")
const TrashPage = screen("trash")
const NotificationsPage = screen("notifications")
const AccountPage = screen("account")
const EditorPage = screen("editor")
const UsersPage = screen("users")
const DrivesPage = screen("drives")
const MonitorPage = screen("monitor")
const SettingsPage = screen("settings")
const AuditPage = screen("audit")
const PublicSharePage = screen("publicShare")
const BackupsPage = screen("backups")
const SnapshotPage = screen("snapshot")
const CollectionsPage = screen("collections")
const CollectionPage = screen("collection")
const TagsPage = screen("tags")
const LoginPage = screen("login")

export function useConfig() {
  return useQuery({ queryKey: ["config"], queryFn: api.config, staleTime: Infinity })
}

/** /files without a place goes to the user's home place. */
function FilesIndex() {
  const { data, isLoading } = usePlaces()
  if (isLoading) return <CenteredSpinner />
  if (!data?.home) return <Navigate to="/shared" replace />
  return <Navigate to={filesUrl(data.home)} replace />
}

const admin = (el: React.ReactNode) => <RequireAdmin>{el}</RequireAdmin>

export default function App() {
  const { data: config, isLoading } = useConfig()

  useEffect(() => {
    if (!config) return
    applyAccent(config.accentColor)
    document.title = config.appName
  }, [config])

  if (isLoading) return <CenteredSpinner />

  return (
    <Routes>
      <Route path="/s/:token/*" element={<PublicSharePage />} />
      {config?.publicOnly ? (
        <Route path="*" element={<NotFoundPage publicOnly />} />
      ) : (
        <>
          <Route path="/login" element={<LoginPage />} />
          <Route
            element={
              <RequireAuth>
                <AppLayout />
              </RequireAuth>
            }
          >
            <Route path="/home" element={<HomePage />} />
            <Route path="/files" element={<FilesIndex />} />
            <Route path="/files/:place/*" element={<FilesPage />} />
            <Route path="/edit/:place/*" element={<EditorPage />} />
            <Route path="/shared" element={<SharedWithMePage />} />
            <Route path="/shares" element={<SharesPage />} />
            <Route path="/trash" element={<TrashPage />} />
            <Route path="/notifications" element={<NotificationsPage />} />
            <Route path="/account" element={<AccountPage />} />
            <Route path="/collections" element={<CollectionsPage />} />
            <Route path="/collections/:id" element={<CollectionPage />} />
            <Route path="/tags" element={<TagsPage />} />
            <Route path="/tags/:tag" element={<TagsPage />} />
            <Route path="/admin" element={<Navigate to="/admin/users" replace />} />
            <Route path="/admin/users" element={admin(<UsersPage />)} />
            <Route path="/admin/drives" element={admin(<DrivesPage />)} />
            <Route path="/admin/monitor" element={admin(<MonitorPage />)} />
            <Route path="/admin/settings" element={admin(<SettingsPage />)} />
            <Route path="/admin/audit" element={admin(<AuditPage />)} />
            <Route path="/admin/backups" element={admin(<BackupsPage />)} />
            <Route path="/admin/backups/:id/browse" element={admin(<SnapshotPage />)} />
          </Route>
          <Route path="/" element={<Navigate to="/home" replace />} />
          <Route path="*" element={<NotFoundPage />} />
        </>
      )}
    </Routes>
  )
}
