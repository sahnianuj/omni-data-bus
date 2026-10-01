import type { WorkbenchMode } from './store'

// Which view a mapping opens in (OC-062): ?view= wins, then a composed mapping's Composer,
// then the view the user left it in (this browser), then Canvas. Browser storage can be
// unavailable or throw (private windows): every access is guarded.

export const VIEWS: WorkbenchMode[] = ['canvas', 'composer', 'table', 'design']

/** The views in the view bar and "Open in" (OC-067): Table opens on demand only. */
export const BAR_VIEWS: WorkbenchMode[] = ['canvas', 'composer', 'design']

export const isView = (v: string | null | undefined): v is WorkbenchMode => !!v && (VIEWS as string[]).includes(v)

const viewKey = (mappingId: string) => `odb.view.${mappingId}`

export function rememberedView(mappingId: string): WorkbenchMode | undefined {
  try {
    const v = window.localStorage.getItem(viewKey(mappingId))
    return isView(v) ? v : undefined
  } catch {
    return undefined
  }
}

export function rememberView(mappingId: string, mode: WorkbenchMode) {
  try {
    window.localStorage.setItem(viewKey(mappingId), mode)
  } catch {
    // best effort
  }
}

/** The view to open: the link's, the Composer for a composed mapping, the remembered one, else Canvas. */
export function initialView(mappingId: string, composed: boolean, search: string): WorkbenchMode {
  const linked = new URLSearchParams(search).get('view')
  if (isView(linked)) return linked
  if (composed) return 'composer'
  const remembered = rememberedView(mappingId)
  return remembered && remembered !== 'table' ? remembered : 'canvas'
}

const hintKey = 'odb.hint.composer'

export function composerHintDismissed(): boolean {
  try {
    return window.localStorage.getItem(hintKey) === 'off'
  } catch {
    return false
  }
}

export function dismissComposerHint() {
  try {
    window.localStorage.setItem(hintKey, 'off')
  } catch {
    // best effort
  }
}
