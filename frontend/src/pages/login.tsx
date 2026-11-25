import { useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Navigate, useNavigate, useSearchParams } from "react-router"

import { useConfig } from "@/App"
import { useMe } from "@/components/app-layout"
import { BrandMark } from "@/components/brand"
import { Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp"
import { api, ApiError } from "@/lib/api"

export default function LoginPage() {
  const { data: config } = useConfig()
  const me = useMe()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [needCode, setNeedCode] = useState(false)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  const next = params.get("next")
  const dest = next && next.startsWith("/") && !next.startsWith("//") && !next.includes("\\") && ![...next].some((c) => c <= " ") ? next : "/home"
  if (me.data) return <Navigate to={dest} replace />

  const submit = async (ev?: FormEvent, otp = code) => {
    ev?.preventDefault()
    setBusy(true)
    setError("")
    try {
      const user = await api.login(username, password, needCode ? otp : undefined)
      qc.setQueryData(["me"], user)
      navigate(dest, { replace: true })
    } catch (e) {
      const err = e as ApiError
      if (err.code === "totp_required") {
        setNeedCode(true)
      } else {
        setError(err.message)
        if (err.code === "invalid_code") setCode("")
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-6 bg-muted/40 p-6">
      <div className="flex items-center gap-2.5 text-lg font-semibold tracking-tight">
        <BrandMark />
        {config?.appName}
      </div>
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>{needCode ? "Two-factor authentication" : "Sign in"}</CardTitle>
          <CardDescription>
            {needCode ? "Enter the 6-digit code from your authenticator app." : config?.loginMessage || "Sign in to access your files."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit}>
            <FieldGroup>
              {needCode ? (
                <Field className="items-center">
                  <InputOTP
                    maxLength={6}
                    value={code}
                    autoFocus
                    inputMode="numeric"
                    onChange={(v) => {
                      setCode(v)
                      if (v.length === 6) submit(undefined, v)
                    }}
                  >
                    <InputOTPGroup>
                      {Array.from({ length: 6 }).map((_, i) => (
                        <InputOTPSlot key={i} index={i} />
                      ))}
                    </InputOTPGroup>
                  </InputOTP>
                </Field>
              ) : (
                <>
                  <Field>
                    <FieldLabel htmlFor="username">Username</FieldLabel>
                    <Input
                      id="username"
                      autoComplete="username"
                      autoCapitalize="none"
                      autoFocus
                      required
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="password">Password</FieldLabel>
                    <Input
                      id="password"
                      type="password"
                      autoComplete="current-password"
                      required
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                    />
                  </Field>
                </>
              )}
              {error && <FieldError>{error}</FieldError>}
              <Button type="submit" className="w-full" disabled={busy || (needCode && code.length < 6)}>
                {busy && <Spinner />}
                {needCode ? "Verify" : "Sign in"}
              </Button>
              {needCode && (
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    setNeedCode(false)
                    setCode("")
                    setError("")
                  }}
                >
                  Back
                </Button>
              )}
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
