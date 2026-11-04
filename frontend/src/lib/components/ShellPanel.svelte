<script lang="ts">
  import { DeviceService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import { app, errMsg } from '../state.svelte'
  import { t } from '../i18n/index.svelte'

  type Block = { cmd: string; out: string; code: number | null; device: string }
  let blocks = $state<Block[]>([])
  let cmd = $state('')
  let busy = $state(false)
  let history: string[] = []
  let hIdx = -1
  let out: HTMLDivElement

  async function run(e: Event) {
    e.preventDefault()
    const c = cmd.trim()
    const d = app.selected
    if (!c || !d) return
    history = [c, ...history.filter(h => h !== c)].slice(0, 100)
    hIdx = -1
    cmd = ''
    busy = true
    const b: Block = { cmd: c, out: '', code: null, device: d.name }
    blocks = [...blocks.slice(-199), b]
    try {
      const r = await DeviceService.Shell(d.id, c)
      b.out = r.output
      b.code = r.exitCode
    } catch (err) {
      b.out = errMsg(err)
      b.code = -1
    }
    blocks = [...blocks]
    busy = false
    queueMicrotask(() => out && (out.scrollTop = out.scrollHeight))
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'ArrowUp' && hIdx < history.length - 1) { hIdx++; cmd = history[hIdx]; e.preventDefault() }
    if (e.key === 'ArrowDown') { hIdx = Math.max(-1, hIdx - 1); cmd = hIdx < 0 ? '' : history[hIdx]; e.preventDefault() }
    if (e.key === 'l' && e.ctrlKey) { blocks = []; e.preventDefault() }
  }
</script>

<div class="panel">
  <div class="out mono selectable" bind:this={out}>
    {#each blocks as b}
      <div class="cmd"><span class="faint">{b.device} $</span> {b.cmd}</div>
      {#if b.out}<pre>{b.out}</pre>{/if}
      {#if b.code !== null && b.code !== 0}<div class="rc">exit {b.code}</div>{/if}
    {:else}
      <div class="faint hint">{t('shell.hint')}</div>
    {/each}
  </div>
  <form class="input" onsubmit={run}>
    <span class="mono faint">{app.selected?.name ?? '—'} $</span>
    <input class="mono" bind:value={cmd} onkeydown={onKey} disabled={!app.selected || app.selected.state !== 'online'}
      placeholder={app.selected?.state === 'online' ? 'getprop ro.build.version.release' : t('shell.offline')} spellcheck="false" />
    <button type="submit" disabled={busy || !cmd.trim()}>{busy ? '…' : t('shell.run')}</button>
  </form>
</div>

<style>
  .panel { display: flex; flex-direction: column; height: 100%; }
  .out { flex: 1; overflow: auto; padding: 6px 10px; }
  .cmd { margin-top: 6px; color: var(--accent); }
  pre { margin: 2px 0 0; white-space: pre-wrap; word-break: break-all; font-size: var(--fs-s); }
  .rc { color: var(--err); }
  .hint { font-family: var(--font); padding: 6px 0; }
  .input { display: flex; align-items: center; gap: 6px; padding: 5px 8px; border-top: 1px solid var(--line); flex: none; }
  .input input { flex: 1; }
</style>
