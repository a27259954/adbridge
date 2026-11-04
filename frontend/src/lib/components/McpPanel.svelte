<script lang="ts">
  import { Copy, RotateCw, Power, Globe, Square } from '@lucide/svelte'
  import { MCPService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import { app } from '../state.svelte'
  import { t, i18n, type Key } from '../i18n/index.svelte'

  // Level ids are config values and stay untranslated; the hints are translated.
  const LEVELS: { id: string; hint: Key }[] = [
    { id: 'read', hint: 'mcp.levelRead' },
    { id: 'interact', hint: 'mcp.levelInteract' },
    { id: 'full', hint: 'mcp.levelFull' },
  ]
  const ORIGINS: { id: 'local' | 'remote'; label: Key }[] = [
    { id: 'local', label: 'origin.local' },
    { id: 'remote', label: 'origin.remote' },
  ]

  let now = $state(Date.now())
  $effect(() => {
    const timer = setInterval(() => (now = Date.now()), 1000)
    app.unseenAudit = 0
    return () => clearInterval(timer)
  })

  const m = $derived(app.mcp)
  const tun = $derived(m?.tunnel)
  const stopsIn = $derived(tun?.stopsAt ? Math.max(0, Math.round((tun.stopsAt - now) / 60000)) : 0)
  const remoteHint = $derived(LEVELS.find(l => l.id === m?.remoteLevel)?.hint)

  async function setLevel(origin: string, level: string) {
    if (origin === 'remote' && level === 'full' && !confirm(t('mcp.confirmRemoteFull'))) return
    await app.run(() => MCPService.SetLevel(origin, level))
    await app.refreshMCP()
  }

  async function copyConfig(origin: 'local' | 'remote', field: 'json' | 'claudeCli' | 'secretUrl') {
    await app.run(async () => {
      const cc = await MCPService.ClientConfig(origin)
      await app.copy(cc[field] ?? '', field === 'secretUrl' ? t('mcp.secretCopied') : undefined)
    })
  }

  async function regen() {
    if (!confirm(t('mcp.confirmRegen'))) return
    await app.run(() => MCPService.RegenerateToken(), t('mcp.tokenRegenerated'))
    await app.refreshMCP()
  }

  function fmtTime(ts: unknown) {
    return new Date(ts as string).toLocaleTimeString(i18n.lang, { hour12: false })
  }
</script>

<div class="panel">
  <section class="side">
    <div class="group">
      <h4>{t('mcp.localEndpoint')}</h4>
      <div class="row">
        <span class="dot" class:ok={m?.server.running} class:err={m && !m.server.running}></span>
        <span class="mono selectable">{m?.server.running ? m.server.url : (m?.server.error || t('toolbar.mcpStopped'))}</span>
      </div>
      <div class="row">
        <button onclick={() => copyConfig('local', 'json')} disabled={!m?.server.running}><Copy size={12} /> JSON</button>
        <button onclick={() => copyConfig('local', 'claudeCli')} disabled={!m?.server.running}><Copy size={12} /> claude mcp add</button>
        <button class="danger" title={t('mcp.killSwitch')}
          onclick={async () => { await app.run(() => MCPService.SetServerRunning(!m?.server.running)); await app.refreshMCP() }}>
          <Power size={12} /> {m?.server.running ? t('common.stop') : t('common.start')}
        </button>
      </div>
    </div>

    <div class="group">
      <h4>{t('mcp.tunnel')} <span class="faint">{tun?.provider}</span></h4>
      <div class="row">
        <span class="dot" class:ok={tun?.state === 'on'} class:busy={tun?.state === 'starting'} class:err={tun?.state === 'error'}></span>
        {#if tun?.state === 'on'}
          <span class="mono selectable url">{tun.url}</span>
        {:else if tun?.state === 'error'}
          <span class="err-text selectable">{tun.error}</span>
        {:else}
          <span class="dim">{tun?.state === 'starting' ? (tun.progress || t('mcp.starting')) : t('common.off')}</span>
        {/if}
      </div>
      <div class="row">
        {#if tun?.state === 'on' || tun?.state === 'starting'}
          <button onclick={() => MCPService.StopTunnel()}><Square size={12} /> {t('common.stop')}</button>
          {#if tun.state === 'on'}
            <button onclick={() => copyConfig('remote', 'json')}><Copy size={12} /> JSON</button>
            <button onclick={() => copyConfig('remote', 'secretUrl')} title={t('mcp.secretUrlTitle')}><Copy size={12} /> {t('mcp.secretUrl')}</button>
          {/if}
        {:else}
          <button class="primary" onclick={() => app.run(() => MCPService.StartTunnel())} disabled={!m?.server.running}><Globe size={12} /> {t('mcp.startTunnel')}</button>
        {/if}
      </div>
      {#if tun?.state === 'on'}
        <div class="faint small">{tun.idleMinutes > 0 ? t('mcp.idleCloses', { min: tun.idleMinutes, left: stopsIn }) : t('mcp.noIdle')}</div>
      {/if}
    </div>

    <div class="group">
      <h4>{t('mcp.permissions')}</h4>
      {#each ORIGINS as origin}
        {@const cur = origin.id === 'local' ? m?.localLevel : m?.remoteLevel}
        <div class="row lvl">
          <span class="who">{t(origin.label)}</span>
          <div class="seg">
            {#each LEVELS as l}
              <button class:active={cur === l.id} title={t(l.hint)} onclick={() => setLevel(origin.id, l.id)}>{l.id}</button>
            {/each}
          </div>
        </div>
      {/each}
      {#if remoteHint}<div class="faint small">{t(remoteHint)} {t('mcp.remoteSuffix')}</div>{/if}
    </div>

    <div class="group">
      <h4>{t('mcp.token')}</h4>
      <div class="row">
        <span class="mono">••••••••{m?.tokenTail}</span>
        <button class="ghost icon" title={t('mcp.copyToken')} onclick={async () => app.copy(await MCPService.Token(), t('mcp.tokenCopied'))}><Copy size={12} /></button>
        <button class="ghost icon" title={t('mcp.regenerate')} onclick={regen}><RotateCw size={12} /></button>
      </div>
    </div>
  </section>

  <section class="activity">
    <div class="head"><h4>{t('mcp.activity')}</h4><span class="faint">{t('mcp.calls', { n: app.audit.length })}</span></div>
    <div class="list">
      <table>
        <tbody>
          {#each [...app.audit].reverse() as e (e.seq)}
            <tr class:bad={!e.ok} title={e.error || e.summary}>
              <td class="mono faint">{fmtTime(e.time)}</td>
              <td><span class="badge" class:on={e.origin === 'remote'}>{e.origin === 'remote' ? t('origin.remote') : t('origin.local')}</span></td>
              <td class="mono tool">{e.tool}</td>
              <td class="dim">{e.device}</td>
              <td class="mono faint r">{e.durationMs}ms</td>
              <td class="mono sum selectable">{e.denied ? `${t('mcp.denied')}: ` : ''}{e.error || e.summary}</td>
            </tr>
          {:else}
            <tr><td class="faint empty">{t('mcp.noCalls')}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </section>
</div>

<style>
  .panel { display: flex; height: 100%; overflow: hidden; }
  .side { width: 380px; flex: none; overflow: auto; border-right: 1px solid var(--line); padding: 4px 10px 10px; }
  .group { padding: 8px 0; border-bottom: 1px solid var(--line); display: grid; gap: 6px; }
  .group:last-child { border-bottom: 0; }
  h4 { margin: 0; font-size: var(--fs-s); font-weight: 600; color: var(--fg-1); text-transform: uppercase; letter-spacing: .04em; }
  .row { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
  .url { overflow: hidden; text-overflow: ellipsis; max-width: 320px; }
  .small { font-size: var(--fs-s); }
  .err-text { color: var(--err); font-size: var(--fs-s); }
  .who { width: 64px; color: var(--fg-1); }
  .activity { flex: 1; display: flex; flex-direction: column; min-width: 0; }
  .head { display: flex; align-items: center; gap: 8px; padding: 8px 10px 6px; }
  .list { flex: 1; overflow: auto; }
  table { width: 100%; border-collapse: collapse; table-layout: fixed; }
  td { height: 22px; padding: 0 8px; border-bottom: 1px solid var(--line); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  td:nth-child(1) { width: 70px; }
  td:nth-child(2) { width: 80px; }
  td:nth-child(3) { width: 110px; }
  td:nth-child(4) { width: 110px; }
  td:nth-child(5) { width: 64px; }
  .r { text-align: right; }
  tr.bad .sum, tr.bad .tool { color: var(--err); }
  .empty { white-space: normal; height: 48px; }
</style>
