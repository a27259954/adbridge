<script lang="ts">
  import { QrCode, KeyRound, Plug, Settings, Globe, Copy } from '@lucide/svelte'
  import { DeviceService, MCPService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import { app } from '../state.svelte'
  import { t } from '../i18n/index.svelte'

  let { onqr, oncode, onsettings, onmcp }: {
    onqr: () => void; oncode: () => void; onsettings: () => void; onmcp: () => void
  } = $props()

  let addr = $state('')
  let busy = $state(false)

  async function connect(e: Event) {
    e.preventDefault()
    if (!addr.trim()) return
    busy = true
    const ok = await app.run(() => DeviceService.Connect(addr).then(() => true), t('toolbar.connected', { addr }))
    busy = false
    if (ok) addr = ''
  }

  const srv = $derived(app.mcp?.server)
  const tun = $derived(app.mcp?.tunnel)
</script>

<div class="bar">
  <button onclick={onqr} title={t('toolbar.pairQrTitle')}><QrCode size={14} /> {t('toolbar.pairQr')}</button>
  <button onclick={oncode} title={t('toolbar.pairCodeTitle')}><KeyRound size={14} /> {t('toolbar.pairCode')}</button>
  <form class="connect" onsubmit={connect}>
    <input class="mono" placeholder="ip[:port]" bind:value={addr} spellcheck="false" />
    <button type="submit" disabled={busy || !addr.trim()}><Plug size={14} /> {t('toolbar.connect')}</button>
  </form>

  <div class="spacer"></div>

  <button class="pill ghost" onclick={onmcp} title={t('toolbar.mcpTitle')}>
    <span class="dot" class:ok={srv?.running} class:err={srv && !srv.running}></span>
    MCP <span class="mono dim">{srv?.running ? srv.addr : t('toolbar.mcpStopped')}</span>
  </button>
  <button class="pill ghost" onclick={onmcp} title={t('toolbar.tunnelTitle')}>
    <Globe size={13} />
    <span class="dot" class:ok={tun?.state === 'on'} class:busy={tun?.state === 'starting'} class:err={tun?.state === 'error'}></span>
    {#if tun?.state === 'on'}
      <span class="mono url">{tun.url}</span>
    {:else}
      <span class="dim">{tun?.state === 'starting' ? (tun.progress || t('toolbar.tunnelStarting')) : tun?.state === 'error' ? t('toolbar.tunnelError') : t('toolbar.tunnelOff')}</span>
    {/if}
  </button>
  {#if tun?.state === 'on'}
    <button class="ghost icon" title={t('toolbar.copyRemote')}
      onclick={() => app.run(async () => app.copy((await MCPService.ClientConfig('remote')).json, t('toolbar.remoteCopied')))}>
      <Copy size={13} />
    </button>
  {/if}
  <button class="ghost icon" onclick={onsettings} title={t('toolbar.settings')}><Settings size={15} /></button>
</div>

<style>
  .bar {
    display: flex; align-items: center; gap: 6px;
    height: 38px; padding: 0 8px; flex: none;
    background: var(--bg-1); border-bottom: 1px solid var(--line);
  }
  .connect { display: flex; gap: 4px; margin-left: 6px; }
  .connect input { width: 170px; }
  .spacer { flex: 1; }
  .pill { gap: 6px; }
  .url { max-width: 260px; overflow: hidden; text-overflow: ellipsis; }
</style>
