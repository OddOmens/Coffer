// The bundled connect page. Talks to the Rust side through two commands:
//   startup -> { savedUrl, lastUrl, version }
//   connect(url) -> validates <url>/api/config, saves it and navigates there.
import { invoke } from "@tauri-apps/api/core"

type Startup = {
  savedUrl: string | null
  lastUrl: string | null
  version: string
}

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T

const views = {
  busy: $("view-busy"),
  offline: $("view-offline"),
  form: $("view-form"),
}

const form = $<HTMLFormElement>("form")
const input = $<HTMLInputElement>("url")
const submit = $<HTMLButtonElement>("submit")
const formError = $<HTMLParagraphElement>("form-error")
const retry = $<HTMLButtonElement>("retry")

let savedUrl: string | null = null

function show(name: keyof typeof views) {
  for (const [key, el] of Object.entries(views)) el.hidden = key !== name
  if (name === "form") input.focus()
}

function message(err: unknown): string {
  if (typeof err === "string") return err
  if (err instanceof Error) return err.message
  return "Something went wrong."
}

function setFormError(text: string | null) {
  formError.textContent = text ?? ""
  formError.hidden = !text
  input.setAttribute("aria-invalid", text ? "true" : "false")
}

// Connects to the saved server; on failure offers retry / change.
async function connectSaved() {
  if (!savedUrl) return show("form")
  $("busy-url").textContent = savedUrl
  show("busy")
  retry.disabled = true
  try {
    await invoke("connect", { url: savedUrl })
    // The window now navigates to the server.
  } catch (err) {
    $("offline-url").textContent = savedUrl
    $("offline-error").textContent = message(err)
    show("offline")
  } finally {
    retry.disabled = false
  }
}

form.addEventListener("submit", async (event) => {
  event.preventDefault()
  const url = input.value.trim()
  if (!url) {
    setFormError("Enter your server's address.")
    return input.focus()
  }
  setFormError(null)
  submit.disabled = true
  input.disabled = true
  submit.textContent = "Connecting…"
  try {
    savedUrl = await invoke<string>("connect", { url })
    submit.textContent = "Opening…"
  } catch (err) {
    setFormError(message(err))
    submit.disabled = false
    input.disabled = false
    submit.textContent = "Connect"
    input.focus()
    input.select()
  }
})

input.addEventListener("input", () => {
  if (!formError.hidden) setFormError(null)
})

retry.addEventListener("click", () => void connectSaved())

$("change").addEventListener("click", () => {
  if (savedUrl) input.value = savedUrl
  setFormError(null)
  show("form")
  input.select()
})

async function init() {
  try {
    const state = await invoke<Startup>("startup")
    $("version").textContent = `Coffer desktop ${state.version}`
    savedUrl = state.savedUrl
    if (state.lastUrl) input.value = state.lastUrl
    if (savedUrl) await connectSaved()
    else show("form")
  } catch (err) {
    show("form")
    setFormError(message(err))
  }
}

void init()
