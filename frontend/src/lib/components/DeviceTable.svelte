<script lang="ts">
  import { Wifi, Usb, MoreHorizontal, RefreshCw } from '@lucide/svelte'
  import { DeviceService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import { app, type Device } from '../state.svelte'
  import { t, tState } from '../i18n/index.svelte'

  let editing = $state('')
  let aliasDraft = $state('')
  let menuFor = $state('')

  function stateDot(d: Device) {
    return d.state === 'online' ? 'ok' : d.state === 'unauthorized' ? 'warn' : d.state === 'offline' ? 'err' : ''
  }

  function startEdit(d: Device) {
    editing = d.id
    aliasDraft = d.alias
  }

  async function saveAlias(d: Device) {
    if (editing !== d.id) return
    editing = ''
    if (aliasDraft !== d.alias) await app.run(() => DeviceService.SetAlias(d.id, aliasDraft))
  }

  async function action(d: Device, what: string) {
    menuFor = ''
    switch (what) {
      case 'wifi':
        await app.run(async () => app.notify(t('dev.nowOnWifi', { addr: await DeviceService.USBToWifi(d.id) })))
        break
      case 'reconnect':
        if (d.lastAddr) await app.run(() => DeviceService.Connect(d.lastAddr), t('toolbar.connected', { addr: d.lastAddr }))
        break
      case 'disconnect':
        await app.run(() => DeviceService.Disconnect(d.id), t('dev.disconnected', { name: d.name }))
        break
      case 'forget':
        await app.run(() => DeviceService.Forget(d.id), t('dev.forgot', { name: d.name }))
        break
    }
  }

  const hasUSB = (d: Device) => (d.transports ?? []).some(t => t.kind === 'usb' && t.state === 'device')
  const hasWifi = (d: Device) => (d.transports ?? []).some(t => t.kind === 'wifi')
</script>

<svelte:window onclick={() => (menuFor = '')} />

<div class="wrap">
  <table>
    <thead>
      <tr>
        <th class="c-dot"></th>
        <th>{t('dev.name')}</th>
        <th>{t('dev.serial')}</th>
        <th class="c-link">{t('dev.link')}</th>
        <th class="c-num">{t('dev.android')}</th>
        <th class="c-num">{t('dev.battery')}</th>
        <th class="c-chk" title={t('dev.aiTitle')}>{t('dev.ai')}</th>
        <th class="c-chk" title={t('dev.autoTitle')}>{t('dev.auto')}</th>
        <th class="c-act"></th>
      </tr>
    </thead>
    <tbody>
      {#each app.devices as d (d.id)}
        <tr class:sel={d.id === app.selectedId} class:off={d.state !== 'online'} onclick={() => (app.selectedId = d.id)}>
          <td class="c-dot"><span class="dot {stateDot(d)}" title={tState(d.state)}></span></td>
          <td class="name" ondblclick={() => startEdit(d)} title={t('dev.renameTitle')}>
            {#if editing === d.id}
              <!-- svelte-ignore a11y_autofocus -->
              <input autofocus bind:value={aliasDraft} placeholder={d.model}
                onblur={() => saveAlias(d)}
                onkeydown={e => { if (e.key === 'Enter') saveAlias(d); if (e.key === 'Escape') editing = '' }} />
            {:else}
              <span>{d.name}</span>
              {#if d.alias && d.model}<span class="faint">{d.model}</span>{/if}
              {#if d.state === 'unauthorized'}<span class="warn-text">{t('dev.acceptRsa')}</span>{/if}
            {/if}
          </td>
          <td class="mono dim selectable">
            {d.active || d.lastAddr || d.id}
            {#if (d.transports?.length ?? 0) > 1}<span class="faint">+{(d.transports?.length ?? 1) - 1}</span>{/if}
          </td>
          <td class="c-link">
            {#each d.transports ?? [] as t (t.serial)}
              <span class="link" class:active={t.serial === d.active} title="{t.serial} · {t.state}">
                {#if t.kind === 'wifi'}<Wifi size={13} />{:else}<Usb size={13} />{/if}
              </span>
            {/each}
            {#if !(d.transports?.length)}<span class="faint">—</span>{/if}
          </td>
          <td class="c-num mono">{d.android ? `${d.android}` : '—'}{#if d.sdk}<span class="faint"> /{d.sdk}</span>{/if}</td>
          <td class="c-num mono" class:low={d.battery >= 0 && d.battery < 20}>{d.battery >= 0 ? `${d.battery}%` : '—'}</td>
          <td class="c-chk"><input type="checkbox" checked={d.aiEnabled}
            onclick={e => e.stopPropagation()}
            onchange={e => app.run(() => DeviceService.SetAIEnabled(d.id, e.currentTarget.checked))} /></td>
          <td class="c-chk"><input type="checkbox" checked={d.autoReconnect} disabled={!d.wireless}
            onclick={e => e.stopPropagation()}
            onchange={e => app.run(() => DeviceService.SetAutoReconnect(d.id, e.currentTarget.checked))} /></td>
          <td class="c-act">
            {#if d.state !== 'online' && d.lastAddr}
              <button class="ghost icon" title={t('dev.reconnect', { addr: d.lastAddr })} onclick={e => { e.stopPropagation(); action(d, 'reconnect') }}><RefreshCw size={13} /></button>
            {/if}
            <button class="ghost icon" title={t('dev.more')} onclick={e => { e.stopPropagation(); menuFor = menuFor === d.id ? '' : d.id }}><MoreHorizontal size={14} /></button>
            {#if menuFor === d.id}
              <div class="menu" role="menu" tabindex="-1" onclick={e => e.stopPropagation()} onkeydown={() => {}}>
                <button class="ghost" disabled={!hasUSB(d)} onclick={() => action(d, 'wifi')}>{t('dev.toWifi')}</button>
                <button class="ghost" onclick={() => startEdit(d)}>{t('dev.rename')}</button>
                <button class="ghost" disabled={!hasWifi(d)} onclick={() => action(d, 'disconnect')}>{t('dev.disconnectWifi')}</button>
                <button class="ghost danger" onclick={() => action(d, 'forget')}>{t('dev.forget')}</button>
              </div>
            {/if}
          </td>
        </tr>
      {:else}
        <tr class="empty"><td colspan="9">
          {t('dev.empty')}
        </td></tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .wrap { overflow: auto; height: 100%; }
  table { width: 100%; border-collapse: collapse; table-layout: fixed; }
  thead th {
    position: sticky; top: 0; z-index: 1;
    height: 24px; padding: 0 8px; text-align: left;
    background: var(--bg-1); border-bottom: 1px solid var(--line);
    font-weight: 500; font-size: var(--fs-s); color: var(--fg-2);
  }
  td { height: var(--row); padding: 0 8px; border-bottom: 1px solid var(--line); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  tbody tr { cursor: default; }
  tbody tr:hover { background: var(--bg-2); }
  tbody tr.sel { background: var(--sel); }
  tr.off td.name > span:first-child { color: var(--fg-1); }
  .c-dot { width: 26px; padding-right: 0; }
  .c-link { width: 64px; }
  .c-num { width: 78px; }
  .c-chk { width: 46px; text-align: center; }
  .c-act { width: 64px; text-align: right; position: relative; overflow: visible; }
  .name { display: flex; align-items: center; gap: 8px; }
  .name input { height: 20px; width: 100%; }
  .link { display: inline-flex; color: var(--fg-2); margin-right: 4px; vertical-align: middle; }
  .link.active { color: var(--accent); }
  .low { color: var(--err); }
  .warn-text { color: var(--warn); font-size: var(--fs-s); }
  .empty td { height: 64px; text-align: center; color: var(--fg-1); }
  .menu {
    position: absolute; right: 6px; top: 24px; z-index: 5;
    display: grid; min-width: 170px; padding: 4px;
    background: var(--bg-1); border: 1px solid var(--line-2); border-radius: 6px;
    box-shadow: 0 8px 24px rgb(0 0 0 / .3);
  }
  .menu button { justify-content: flex-start; width: 100%; }
</style>
