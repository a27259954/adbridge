<script lang="ts">
  import { Camera, RefreshCw } from '@lucide/svelte'
  import { DeviceService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import type { Info } from '../../../bindings/github.com/a27259954/adbridge/internal/device'
  import { app } from '../state.svelte'
  import { t, tState } from '../i18n/index.svelte'

  let info = $state<Info | null>(null)
  let shot = $state('')
  let loading = $state(false)

  async function load() {
    const d = app.selected
    info = null
    shot = ''
    if (!d || d.state !== 'online') return
    loading = true
    info = (await app.run(() => DeviceService.Info(d.id))) ?? null
    loading = false
  }

  async function screenshot() {
    if (!app.selected) return
    shot = (await app.run(() => DeviceService.Screenshot(app.selected!.id))) ?? ''
  }

  // Reload only when the selection or its online state changes, not on
  // every device-list event (battery updates etc.).
  const key = $derived(`${app.selectedId}|${app.selected?.state ?? ''}`)
  $effect(() => {
    key
    load()
  })

  const rows = $derived(info ? [
    [t('info.id'), info.id], [t('info.model'), `${info.manufacturer} ${info.model}`], [t('info.android'), `${info.android} (SDK ${info.sdk})`],
    [t('info.resolution'), info.resolution], [t('info.density'), info.density], [t('info.foreground'), info.foregroundApp],
    [t('info.screen'), info.screenOn ? t('common.on') : t('common.off')], [t('info.abi'), info.abi], [t('info.wifiIp'), info.wifiIp],
    [t('info.patch'), info.securityPatch], [t('info.fingerprint'), info.buildFingerprint],
    [t('info.transports'), (info.transports ?? []).map(tr => `${tr.kind}:${tr.serial} (${tr.state})`).join(', ')],
  ] : [])
</script>

<div class="panel">
  <div class="kv">
    <div class="tools">
      <button onclick={load} disabled={loading || app.selected?.state !== 'online'}><RefreshCw size={13} /> {t('info.refresh')}</button>
      <button onclick={screenshot} disabled={app.selected?.state !== 'online'}><Camera size={13} /> {t('info.screenshot')}</button>
    </div>
    {#if !app.selected}
      <p class="faint">{t('info.selectDevice')}</p>
    {:else if app.selected.state !== 'online'}
      <p class="faint">{t('info.notOnline', { name: app.selected.name, state: tState(app.selected.state) })}</p>
    {:else if loading}
      <p class="faint">{t('common.loading')}</p>
    {:else}
      <table class="selectable">
        <tbody>
          {#each rows as [k, v]}
            <tr><th>{k}</th><td class="mono">{v || '—'}</td></tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
  {#if shot}
    <div class="shot"><img src={shot} alt={t('info.screenshot')} /></div>
  {/if}
</div>

<style>
  .panel { display: flex; height: 100%; overflow: hidden; }
  .kv { flex: 1; overflow: auto; padding: 8px 10px; }
  .tools { display: flex; gap: 6px; margin-bottom: 8px; }
  table { border-collapse: collapse; }
  th { text-align: left; font-weight: 500; color: var(--fg-2); padding: 2px 16px 2px 0; white-space: nowrap; vertical-align: top; }
  td { padding: 2px 0; word-break: break-all; }
  .shot { flex: none; padding: 8px; border-left: 1px solid var(--line); overflow: auto; }
  .shot img { height: 100%; max-height: 100%; border-radius: 6px; border: 1px solid var(--line-2); }
  p { margin: 4px 0; }
</style>
