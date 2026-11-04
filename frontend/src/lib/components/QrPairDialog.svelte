<script lang="ts">
  import { PairService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import { app } from '../state.svelte'
  import { t, type Key } from '../i18n/index.svelte'
  import Dialog from './Dialog.svelte'

  let { open = $bindable(false) } = $props()
  let now = $state(Date.now())

  $effect(() => {
    if (!open) return
    app.run(() => PairService.StartQR()).then(s => s && (app.pairing = s))
    const timer = setInterval(() => (now = Date.now()), 500)
    return () => clearInterval(timer)
  })

  $effect(() => {
    if (open && app.pairing?.state === 'done') {
      app.notify(t('pair.success'))
      setTimeout(() => (open = false), 600)
    }
  })

  const st = $derived(app.pairing)
  const left = $derived(st ? Math.max(0, Math.round((st.expires - now) / 1000)) : 0)
  const label: Record<string, Key> = {
    waiting: 'pair.waiting',
    pairing: 'pair.pairing',
    connecting: 'pair.connecting',
    done: 'pair.done',
    error: 'pair.error',
  }
</script>

<Dialog title={t('pair.qrTitle')} bind:open width={380} onclose={() => PairService.Cancel()}>
  <div class="wrap">
    {#if st?.qr && st.state !== 'idle'}
      <img src={st.qr} alt={t('pair.qrAlt')} class:faded={st.state === 'error'} />
    {:else}
      <div class="ph"></div>
    {/if}
    <div class="status">
      <span class="dot" class:busy={st?.state === 'waiting' || st?.state === 'pairing' || st?.state === 'connecting'}
        class:ok={st?.state === 'done'} class:err={st?.state === 'error'}></span>
      <span>{label[st?.state ?? ''] ? t(label[st!.state]) : t('pair.starting')}</span>
      {#if st?.state === 'waiting'}<span class="faint mono">{left}s</span>{/if}
    </div>
    {#if st?.error}<p class="error selectable">{st.error}</p>{/if}
    <ol class="dim">
      <li>{t('pair.step1')}</li>
      <li>{t('pair.step2')}</li>
      <li>{t('pair.step3')}</li>
    </ol>
  </div>
  {#snippet footer()}
    {#if st?.state === 'error'}
      <button class="primary" onclick={() => app.run(() => PairService.StartQR())}>{t('common.retry')}</button>
    {/if}
    <button onclick={() => { open = false; PairService.Cancel() }}>{t('common.close')}</button>
  {/snippet}
</Dialog>

<style>
  .wrap { display: grid; justify-items: center; gap: 10px; }
  img, .ph { width: 240px; height: 240px; border-radius: 6px; background: #fff; image-rendering: pixelated; }
  .ph { background: var(--bg-2); }
  img.faded { opacity: .3; }
  .status { display: flex; align-items: center; gap: 7px; }
  .error { color: var(--err); margin: 0; font-size: var(--fs-s); text-align: center; }
  ol { margin: 0; padding-left: 18px; font-size: var(--fs-s); justify-self: stretch; display: grid; gap: 2px; }
</style>
