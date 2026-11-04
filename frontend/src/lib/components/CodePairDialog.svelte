<script lang="ts">
  import { DeviceService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import { app } from '../state.svelte'
  import Dialog from './Dialog.svelte'
  import { t } from '../i18n/index.svelte'

  let { open = $bindable(false) } = $props()
  let addr = $state('')
  let code = $state('')
  let busy = $state(false)

  async function pair(e: Event) {
    e.preventDefault()
    busy = true
    const ok = await app.run(() => DeviceService.Pair(addr, code).then(() => true), t('pair.success'))
    busy = false
    if (ok) { open = false; code = '' }
  }
</script>

<Dialog title={t('pair.codeTitle')} bind:open width={360}>
  <form id="pair-code" onsubmit={pair} class="form">
    <label class="field">{t('pair.addr')}
      <input class="mono" placeholder="192.168.1.23:37011" bind:value={addr} required />
    </label>
    <label class="field">{t('pair.code')}
      <input class="mono" placeholder="123456" inputmode="numeric" maxlength="6" bind:value={code} required />
    </label>
    <p class="dim hint">{t('pair.codeHint')}</p>
  </form>
  {#snippet footer()}
    <button onclick={() => (open = false)}>{t('common.cancel')}</button>
    <button class="primary" type="submit" form="pair-code" disabled={busy}>{busy ? t('pair.pairing') : t('pair.pair')}</button>
  {/snippet}
</Dialog>

<style>
  .form { display: grid; gap: 10px; }
  .hint { margin: 0; font-size: var(--fs-s); }
</style>
