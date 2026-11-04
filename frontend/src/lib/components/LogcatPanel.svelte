<script lang="ts">
  import { Pause, Play, Trash2, ArrowDownToLine } from '@lucide/svelte'
  import { LogService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import type { LogEntry } from '../../../bindings/github.com/a27259954/adbridge/internal/device'
  import { app } from '../state.svelte'
  import { t } from '../i18n/index.svelte'

  const ROW = 18
  const KEEP = 20000
  const LEVELS = ['V', 'D', 'I', 'W', 'E', 'F']
  const RANK: Record<string, number> = { V: 0, D: 1, I: 2, W: 3, E: 4, F: 5, S: 6 }

  let entries = $state.raw<LogEntry[]>([])
  let lastSeq = 0
  let deviceId = ''
  let paused = $state(false)
  let follow = $state(true)
  let minLevel = $state('V')
  let tag = $state('')
  let text = $state('')

  let viewport: HTMLDivElement
  let scrollTop = $state(0)
  let height = $state(300)

  const filtered = $derived.by(() => {
    const min = RANK[minLevel]
    const t = tag.toLowerCase()
    const q = text.toLowerCase()
    if (min === 0 && !t && !q) return entries
    return entries.filter(e =>
      RANK[e.level] >= min &&
      (!t || e.tag.toLowerCase().includes(t)) &&
      (!q || e.msg.toLowerCase().includes(q) || e.tag.toLowerCase().includes(q)))
  })

  const start = $derived(Math.max(0, Math.floor(scrollTop / ROW) - 10))
  const end = $derived(Math.min(filtered.length, Math.ceil((scrollTop + height) / ROW) + 10))
  const visible = $derived(filtered.slice(start, end))

  async function poll() {
    const id = app.selectedId
    if (id !== deviceId) {
      deviceId = id
      lastSeq = 0
      entries = []
    }
    if (!id || paused) return
    const batch = (await LogService.Since(id, lastSeq, 5000)) ?? []
    if (id !== deviceId || batch.length === 0) return
    lastSeq = batch[batch.length - 1].seq
    const next = entries.concat(batch)
    entries = next.length > KEEP ? next.slice(next.length - KEEP) : next
  }

  $effect(() => {
    const t = setInterval(poll, 400)
    poll()
    return () => clearInterval(t)
  })

  // Stick to the bottom while following.
  $effect(() => {
    filtered.length
    if (follow && viewport) queueMicrotask(() => (viewport.scrollTop = viewport.scrollHeight))
  })

  function onScroll() {
    scrollTop = viewport.scrollTop
    const atBottom = viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - ROW * 2
    if (follow !== atBottom) follow = atBottom
  }

  async function clear() {
    if (!app.selectedId) return
    await app.run(() => LogService.Clear(app.selectedId))
    entries = []
  }
</script>

<div class="panel">
  <div class="tools">
    <div class="seg">
      {#each LEVELS as l}
        <button class:active={minLevel === l} class="lvl-{l}" onclick={() => (minLevel = l)} title={t('log.minLevel', { level: l })}>{l}</button>
      {/each}
    </div>
    <input class="mono" placeholder={t('log.tag')} bind:value={tag} style="width:130px" spellcheck="false" />
    <input class="mono" placeholder={t('log.filter')} bind:value={text} style="width:220px" spellcheck="false" />
    <div class="spacer"></div>
    <span class="faint mono">{filtered.length.toLocaleString()} / {entries.length.toLocaleString()}</span>
    <button class="ghost icon" title={paused ? t('log.resume') : t('log.pause')} onclick={() => (paused = !paused)}>
      {#if paused}<Play size={14} />{:else}<Pause size={14} />{/if}
    </button>
    <button class="ghost icon" class:on={follow} title={t('log.follow')} onclick={() => { follow = true; viewport.scrollTop = viewport.scrollHeight }}><ArrowDownToLine size={14} /></button>
    <button class="ghost icon" title={t('log.clear')} onclick={clear}><Trash2 size={14} /></button>
  </div>
  <div class="viewport mono selectable" bind:this={viewport} bind:clientHeight={height} onscroll={onScroll}>
    {#if !app.selectedId}
      <div class="empty">{t('log.selectDevice')}</div>
    {:else}
      <div style:height="{filtered.length * ROW}px" class="spacer-y">
        <div style:transform="translateY({start * ROW}px)">
          {#each visible as e (e.seq)}
            <div class="line l-{e.level}">
              <span class="t">{e.time.slice(6)}</span><span class="p">{e.pid}</span><span class="lv">{e.level}</span><span class="tag" title={e.tag}>{e.tag}</span><span class="msg">{e.msg}</span>
            </div>
          {/each}
        </div>
      </div>
    {/if}
  </div>
</div>

<style>
  .panel { display: flex; flex-direction: column; height: 100%; }
  .tools { display: flex; align-items: center; gap: 6px; padding: 5px 8px; border-bottom: 1px solid var(--line); flex: none; }
  .tools .seg button { width: 24px; padding: 0; justify-content: center; font-family: var(--mono); }
  .spacer { flex: 1; }
  .on { color: var(--accent); }
  .viewport { flex: 1; overflow: auto; position: relative; background: var(--bg); }
  .spacer-y { position: relative; }
  .line { height: 18px; line-height: 18px; display: flex; gap: 8px; padding: 0 8px; white-space: pre; }
  .line:hover { background: var(--bg-2); }
  .t { color: var(--fg-2); flex: none; }
  .p { color: var(--fg-2); width: 44px; text-align: right; flex: none; }
  .lv { width: 10px; flex: none; font-weight: 700; }
  .tag { width: 150px; flex: none; overflow: hidden; text-overflow: ellipsis; color: var(--fg-1); }
  .msg { flex: 1; min-width: 0; }
  .l-V .lv, .l-V .msg { color: var(--fg-2); }
  .l-D .lv { color: var(--debug); }
  .l-I .lv { color: var(--ok); }
  .l-W .lv, .l-W .msg { color: var(--warn); }
  .l-E .lv, .l-E .msg { color: var(--err); }
  .l-F .lv, .l-F .msg { color: var(--fatal); }
  .empty { padding: 20px; color: var(--fg-2); font-family: var(--font); }
</style>
