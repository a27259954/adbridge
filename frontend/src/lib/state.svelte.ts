// Global UI state (Svelte 5 runes) fed by Go events.
import { Events, Clipboard } from '@wailsio/runtime'
import { DeviceService, MCPService, PairService } from '../../bindings/github.com/a27259954/adbridge/internal/app'
import type { MCPState } from '../../bindings/github.com/a27259954/adbridge/internal/app'
import type { Device } from '../../bindings/github.com/a27259954/adbridge/internal/device'
import type { Status as PairStatus } from '../../bindings/github.com/a27259954/adbridge/internal/pairing'
import type { Entry as AuditEntry } from '../../bindings/github.com/a27259954/adbridge/internal/audit'
import { t } from './i18n/index.svelte'

export type { Device, PairStatus, AuditEntry, MCPState }

class AppState {
  devices = $state<Device[]>([])
  selectedId = $state<string>('')
  mcp = $state<MCPState | null>(null)
  pairing = $state<PairStatus | null>(null)
  audit = $state<AuditEntry[]>([])
  unseenAudit = $state(0)
  toast = $state<{ msg: string; kind: 'ok' | 'err' } | null>(null)

  selected = $derived(this.devices.find(d => d.id === this.selectedId) ?? null)
  online = $derived(this.devices.filter(d => d.state === 'online').length)

  #toastTimer: ReturnType<typeof setTimeout> | undefined

  async init() {
    Events.On('devices', ev => this.setDevices(ev.data ?? []))
    Events.On('pairing', ev => { this.pairing = ev.data })
    Events.On('tunnel', () => this.refreshMCP())
    Events.On('mcp', () => this.refreshMCP())
    Events.On('audit', ev => {
      this.audit = [...this.audit.slice(-499), ev.data]
      this.unseenAudit++
    })
    this.setDevices((await DeviceService.List()) ?? [])
    this.audit = (await MCPService.AuditSince(0)) ?? []
    this.pairing = await PairService.Status()
    await this.refreshMCP()
  }

  setDevices(list: Device[]) {
    this.devices = list
    if (!list.some(d => d.id === this.selectedId)) {
      this.selectedId = (list.find(d => d.state === 'online') ?? list[0])?.id ?? ''
    }
  }

  async refreshMCP() {
    this.mcp = await MCPService.State()
  }

  notify(msg: string, kind: 'ok' | 'err' = 'ok') {
    this.toast = { msg, kind }
    clearTimeout(this.#toastTimer)
    this.#toastTimer = setTimeout(() => (this.toast = null), kind === 'err' ? 6000 : 2500)
  }

  /** Runs fn, reporting failures as a toast. Returns undefined on error. */
  async run<T>(fn: () => Promise<T>, okMsg?: string): Promise<T | undefined> {
    try {
      const r = await fn()
      if (okMsg) this.notify(okMsg)
      return r
    } catch (e) {
      this.notify(errMsg(e), 'err')
      return undefined
    }
  }

  async copy(text: string, what?: string) {
    await Clipboard.SetText(text)
    this.notify(what ?? t('common.copied'))
  }
}

export function errMsg(e: unknown): string {
  if (e && typeof e === 'object' && 'message' in e) return String((e as Error).message)
  return String(e)
}

export const app = new AppState()
