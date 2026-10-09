<script lang="ts">
  import { app } from "../../lib/state.svelte";

  let { id, label }: { id: string; label: string } = $props();
  const known = $derived.by(() => {
    void app.graph?.version;
    return app.graph?.nodes.has(id) ?? false;
  });
</script>

{#if known}
  <button class="link" onclick={() => app.select(id, true)}>{label}</button>
{:else}
  <span>{label}</span>
{/if}

<style>
  .link {
    background: none;
    border: 0;
    padding: 0;
    color: var(--accent);
    cursor: pointer;
    text-align: left;
    font: inherit;
  }
  .link:hover {
    text-decoration: underline;
  }
</style>
