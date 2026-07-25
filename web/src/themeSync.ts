// Synchronize this plugin UI's theme with the official CPA management panel.
//
// The panel sets `data-theme` ("white" | "dark", or absent = light) on its own
// document.documentElement. This iframe mirrors that attribute and maps it onto
// Semi Design's body theme class so the admin UI follows the host panel.
//
// Standalone opens (self === top) fall back to light / system preference is not
// applied — matches key-policy themeSync policy.

const THEME_ATTR = 'data-theme'
const PANEL_THEME_STORAGE_KEY = 'cli-proxy-theme'

export type AppliedTheme = 'light' | 'white' | 'dark'

let observer: MutationObserver | null = null
let storageHandler: ((e: StorageEvent) => void) | null = null
let started = false

function isEmbedded(): boolean {
  try {
    return window.self !== window.top
  } catch {
    return false
  }
}

function readParentTheme(): AppliedTheme | null {
  if (!isEmbedded()) return null
  let parentEl: HTMLElement | null
  try {
    parentEl = window.parent.document.documentElement
  } catch {
    return null
  }
  const raw = parentEl.getAttribute(THEME_ATTR)
  if (raw === 'white' || raw === 'dark') return raw
  return 'light'
}

function applyThemeToSelf(theme: AppliedTheme): void {
  const el = document.documentElement
  if (theme === 'white' || theme === 'dark') {
    el.setAttribute(THEME_ATTR, theme)
  } else {
    el.removeAttribute(THEME_ATTR)
  }
  // Semi Design body theme classes.
  const body = document.body
  body.removeAttribute('theme-mode')
  if (theme === 'dark') {
    body.setAttribute('theme-mode', 'dark')
  }
}

function sync(): void {
  applyThemeToSelf(readParentTheme() ?? 'light')
}

export function initThemeSync(): void {
  if (started) return
  started = true
  sync()
  if (!isEmbedded()) return

  let parentEl: HTMLElement
  try {
    parentEl = window.parent.document.documentElement
  } catch {
    return
  }
  observer = new MutationObserver(() => sync())
  observer.observe(parentEl, { attributes: true, attributeFilter: [THEME_ATTR] })

  storageHandler = (e: StorageEvent) => {
    if (e.key === PANEL_THEME_STORAGE_KEY) sync()
  }
  window.addEventListener('storage', storageHandler)
}

export function _teardownThemeSync(): void {
  observer?.disconnect()
  observer = null
  if (storageHandler) {
    window.removeEventListener('storage', storageHandler)
    storageHandler = null
  }
  started = false
  document.documentElement.removeAttribute(THEME_ATTR)
  document.body.removeAttribute('theme-mode')
}
