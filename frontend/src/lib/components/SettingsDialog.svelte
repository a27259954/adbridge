<script lang="ts">
  import { Eye, EyeOff, FolderOpen } from '@lucide/svelte'
  import { SettingsService, DeviceService } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import type { ADBStatus } from '../../../bindings/github.com/a27259954/adbridge/internal/app'
  import type { Config } from '../../../bindings/github.com/a27259954/adbridge/internal/config'
  import { app } from '../state.svelte'
  import { t, i18n, LANGUAGES } from '../i18n/index.svelte'
  import Dialog from './Dialog.svelte'

  let { open = $bindable(false) } = $props()
  let cfg = $state<Config | null>(null)
  let adb = $state<ADBStatus | null>(null)
  let version = $state('')
  let reveal = $state(false)
  let saving = $state(false)

  $effect(() => {
    if (!open) return
    SettingsService.Get().then(c => (cfg = c))
    DeviceService.ADBStatus().then(s => (adb = s))
    SettingsService.Version().then(v => (version = v))
  })

  async function save() {
    if (!cfg) return
    saving = true
    const ok = await app.run(() => SettingsService.Save($state.snapshot(cfg) as Config).then(() => true))
    saving = false
    if (ok) {
      i18n.set(await SettingsService.Language())
      app.notify(t('set.saved'))
      open = false
      app.refreshMCP()
    }
  }
</script>

<Dialog title={t('set.title')} bind:open width={540}>
  {#if cfg}
    <fieldset>
      <legend>{t('set.general')}</legend>
      <label class="field">{t('set.language')}
        <select bind:value={cfg.language}>
          <option value="">{t('set.languageAuto')}</option>
          {#each LANGUAGES as l}
            <option value={l.code}>{l.name}</option>
          {/each}
        </select>
      </label>
    </fieldset>

    <fieldset>
      <legend>{t('set.adb')}</legend>
      <label class="field">{t('set.adbPath')} <span class="faint">{t('set.adbPathHint')}</span>
        <input class="mono" bind:value={cfg.adbPath} placeholder={adb?.path || 'C:\\…\\platform-tools\\adb.exe'} spellcheck="false" />
      </label>
      <div class="dim small mono selectable">
        {#if adb?.path}{adb.path} · {adb.version || '?'}{/if}
        {#if adb?.error}<span class="err">{adb.error}</span>{/if}
      </div>
    </fieldset>

    <fieldset>
      <legend>{t('set.mcp')}</legend>
      <div class="grid2">
        <label class="field">{t('set.listen')}
          <input class="mono" bind:value={cfg.mcp.addr} spellcheck="false" />
        </label>
        <label class="field">{t('set.maxOutput')}
          <input class="mono" type="number" min="1000" step="1000" bind:value={cfg.mcp.maxOutputChars} />
        </label>
      </div>
      <label class="check"><input type="checkbox" bind:checked={cfg.mcp.requireTokenLocal} /> {t('set.requireToken')}</label>
    </fieldset>

    <fieldset>
      <legend>{t('set.tunnel')}</legend>
      <div class="grid2">
        <label class="field">{t('set.provider')}
          <select bind:value={cfg.tunnel.provider}>
            <option value="ngrok">{t('set.providerNgrok')}</option>
            <option value="cloudflared">{t('set.providerCf')}</option>
          </select>
        </label>
        <label class="field">{t('set.idle')}
          <input class="mono" type="number" min="0" bind:value={cfg.tunnel.idleMinutes} />
        </label>
      </div>
      {#if cfg.tunnel.provider === 'ngrok'}
        <label class="field">{t('set.ngrokToken')} <span class="faint">{t('set.ngrokTokenHint')}</span>
          <div class="pw">
            <input class="mono" type={reveal ? 'text' : 'password'} bind:value={cfg.tunnel.ngrokAuthtoken} spellcheck="false" />
            <button class="ghost icon" onclick={() => (reveal = !reveal)} title={t('set.show')}>{#if reveal}<EyeOff size={13} />{:else}<Eye size={13} />{/if}</button>
          </div>
        </label>
        <label class="field">{t('set.domain')} <span class="faint">{t('set.domainHint')}</span>
          <input class="mono" bind:value={cfg.tunnel.ngrokDomain} spellcheck="false" />
        </label>
      {:else}
        <p class="dim small">{t('set.cfNote')}</p>
      {/if}
    </fieldset>

    <div class="row">
      <button onclick={() => SettingsService.OpenConfigDir()}><FolderOpen size={13} /> {t('set.configFolder')}</button>
      <button onclick={() => SettingsService.OpenDataDir()}><FolderOpen size={13} /> {t('set.dataFolder')}</button>
      <span class="spacer"></span>
      <span class="faint mono">adbridge {version}</span>
    </div>
  {/if}
  {#snippet footer()}
    <button onclick={() => (open = false)}>{t('common.cancel')}</button>
    <button class="primary" onclick={save} disabled={saving}>{t('common.save')}</button>
  {/snippet}
</Dialog>

<style>
  fieldset { border: 1px solid var(--line); border-radius: 6px; padding: 8px 10px 10px; margin: 0; display: grid; gap: 8px; min-width: 0; }
  fieldset input, fieldset select { min-width: 0; width: 100%; }
  fieldset input[type="checkbox"] { width: auto; }
  legend { padding: 0 4px; color: var(--fg-1); font-size: var(--fs-s); text-transform: uppercase; letter-spacing: .04em; }
  .grid2 { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 8px; align-items: end; }
  .check { display: flex; align-items: center; gap: 6px; }
  .pw { display: flex; gap: 4px; }
  .pw input { flex: 1; }
  .small { font-size: var(--fs-s); margin: 0; }
  .err { color: var(--err); }
  .row { display: flex; gap: 6px; align-items: center; }
  .spacer { flex: 1; }
</style>
