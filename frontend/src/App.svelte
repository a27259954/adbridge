<script lang="ts">
  import { onMount } from 'svelte'
  import { DeviceService, SettingsService } from '../bindings/github.com/a27259954/adbridge/internal/app'
  import type { ADBStatus } from '../bindings/github.com/a27259954/adbridge/internal/app'
  import { app } from './lib/state.svelte'
  import { t, i18n, tState, type Key } from './lib/i18n/index.svelte'
  import Toolbar from './lib/components/Toolbar.svelte'
  import DeviceTable from './lib/components/DeviceTable.svelte'
  import LogcatPanel from './lib/components/LogcatPanel.svelte'
  import ShellPanel from './lib/components/ShellPanel.svelte'
  import InfoPanel from './lib/components/InfoPanel.svelte'
  import McpPanel from './lib/components/McpPanel.svelte'
  import QrPairDialog from './lib/components/QrPairDialog.svelte'
  import CodePairDialog from './lib/components/CodePairDialog.svelte'
  import SettingsDialog from './lib/components/SettingsDialog.svelte'

  type Tab = 'logcat' | 'shell' | 'info' | 'mcp'
  let tab = $state<Tab>(loadTab())
  let qrOpen = $state(false)
  let codeOpen = $state(false)
  let settingsOpen = $state(false)
  let adb = $state<ADBStatus | null>(null)

  // Resizable split between the device table and the bottom panel.
  let split = $state(loadSplit())
  let dragging = false

  function loadTab(): Tab {
    try { return (localStorage.getItem('tab') as Tab) || 'logcat' } catch { return 'logcat' }
  }
  function loadSplit(): number {
    try { return Number(localStorage.getItem('split')) || 210 } catch { return 210 }
  }
  $effect(() => { try { localStorage.setItem('tab', tab) } catch {} })

  function onMove(e: MouseEvent) {
    if (!dragging) return
    split = Math.min(Math.max(e.clientY - 38, 90), window.innerHeight - 200)
  }
  function onUp() {
    if (!dragging) return
    dragging = false
    try { localStorage.setItem('split', String(split)) } catch {}
  }

  onMount(() => {
    SettingsService.Language().then(l => i18n.set(l))
    app.init()
    const load = () => DeviceService.ADBStatus().then(s => (adb = s))
    load()
    const t = setInterval(load, 10000)
    return () => clearInterval(t)
  })

  const tabs: { id: Tab; label: Key }[] = [
    { id: 'logcat', label: 'tab.logcat' },
    { id: 'shell', label: 'tab.shell' },
    { id: 'info', label: 'tab.info' },
    { id: 'mcp', label: 'tab.mcp' },
  ]
</script>

<svelte:window onmousemove={onMove} onmouseup={onUp} />

<div class="app">
  <Toolbar onqr={() => (qrOpen = true)} oncode={() => (codeOpen = true)} onsettings={() => (settingsOpen = true)} onmcp={() => (tab = 'mcp')} />

  <div class="devices" style:height="{split}px"><DeviceTable /></div>
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="splitter" role="separator" aria-orientation="horizontal" onmousedown={() => (dragging = true)}></div>

  <div class="bottom">
    <nav>
      {#each tabs as tb}
        <button class="tab" class:active={tab === tb.id} onclick={() => (tab = tb.id)}>
          {t(tb.label)}
          {#if tb.id === 'mcp' && app.unseenAudit > 0 && tab !== 'mcp'}<span class="count">{app.unseenAudit}</span>{/if}
        </button>
      {/each}
      <span class="spacer"></span>
      {#if tab !== 'mcp'}<span class="dim target">{app.selected ? `${app.selected.name} · ${app.selected.active || tState(app.selected.state)}` : t('app.noDevice')}</span>{/if}
    </nav>
    <div class="content">
      {#if tab === 'logcat'}<LogcatPanel />
      {:else if tab === 'shell'}<ShellPanel />
      {:else if tab === 'info'}<InfoPanel />
      {:else}<McpPanel />{/if}
    </div>
  </div>

  <footer class="status mono">
    <span class="dot" class:ok={adb?.serverOk} class:err={adb && !adb.serverOk}></span>
    {#if adb?.serverOk}
      <span>adb {adb.version}</span>
    {:else}
      <span class="err selectable">{adb?.error || t('app.adbUnreachable')}</span>
    {/if}
    <span class="faint">·</span>
    <span>{t('app.online', { n: app.online, total: app.devices.length })}</span>
    <span class="spacer"></span>
    {#if app.toast}<span class="toast" class:err={app.toast.kind === 'err'}>{app.toast.msg}</span>{/if}
  </footer>
</div>

<QrPairDialog bind:open={qrOpen} />
<CodePairDialog bind:open={codeOpen} />
<SettingsDialog bind:open={settingsOpen} />

<style>
  .app { display: flex; flex-direction: column; height: 100%; }
  .devices { flex: none; overflow: hidden; background: var(--bg-1); }
  .splitter { height: 5px; flex: none; cursor: row-resize; background: var(--bg); border-top: 1px solid var(--line); border-bottom: 1px solid var(--line); }
  .splitter:hover { background: var(--accent-bg); }
  .bottom { flex: 1; display: flex; flex-direction: column; min-height: 0; background: var(--bg-1); }
  nav { display: flex; align-items: stretch; height: 30px; border-bottom: 1px solid var(--line); flex: none; padding-left: 4px; }
  .tab { height: auto; border: 0; border-radius: 0; background: transparent; color: var(--fg-1); padding: 0 12px; border-bottom: 2px solid transparent; }
  .tab:hover:not(:disabled) { background: transparent; color: var(--fg); }
  .tab.active { color: var(--fg); border-bottom-color: var(--accent); }
  .count { background: var(--accent); color: #fff; border-radius: 8px; padding: 0 5px; font-size: 10px; line-height: 15px; }
  .spacer { flex: 1; }
  .target { align-self: center; padding-right: 10px; font-size: var(--fs-s); }
  .content { flex: 1; min-height: 0; }
  .status { display: flex; align-items: center; gap: 8px; height: 22px; padding: 0 8px; flex: none; background: var(--bg); border-top: 1px solid var(--line); color: var(--fg-1); }
  .status .err { color: var(--err); }
  .toast { color: var(--ok); font-family: var(--font); }
  .toast.err { color: var(--err); }
</style>
