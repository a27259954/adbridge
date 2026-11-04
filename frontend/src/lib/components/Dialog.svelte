<script lang="ts">
  import type { Snippet } from 'svelte'
  import { X } from '@lucide/svelte'
  import { t } from '../i18n/index.svelte'

  let { title, open = $bindable(false), width = 420, onclose, children, footer }: {
    title: string
    open?: boolean
    width?: number
    onclose?: () => void
    children: Snippet
    footer?: Snippet
  } = $props()

  function close() {
    open = false
    onclose?.()
  }
</script>

<svelte:window onkeydown={e => open && e.key === 'Escape' && close()} />

{#if open}
  <div class="backdrop" role="presentation" onmousedown={e => e.target === e.currentTarget && close()}>
    <div class="dialog" style:width="{width}px" role="dialog" aria-label={title}>
      <header>
        <span>{title}</span>
        <button class="ghost icon" onclick={close} aria-label={t('common.close')}><X size={14} /></button>
      </header>
      <div class="body">{@render children()}</div>
      {#if footer}<footer>{@render footer()}</footer>{/if}
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed; inset: 0; z-index: 50;
    background: rgb(0 0 0 / .45);
    display: grid; place-items: center;
  }
  .dialog {
    max-width: calc(100vw - 32px); max-height: calc(100vh - 32px);
    display: flex; flex-direction: column;
    background: var(--bg-1); border: 1px solid var(--line-2); border-radius: 7px;
    box-shadow: 0 12px 40px rgb(0 0 0 / .35);
  }
  header {
    display: flex; align-items: center; justify-content: space-between;
    height: 34px; padding: 0 6px 0 12px; border-bottom: 1px solid var(--line);
    font-weight: 600;
  }
  .body { padding: 12px; overflow: auto; display: grid; gap: 10px; }
  footer {
    display: flex; justify-content: flex-end; gap: 6px;
    padding: 8px 12px; border-top: 1px solid var(--line);
  }
</style>
