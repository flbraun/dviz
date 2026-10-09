<script lang="ts">
  import { app } from "../lib/state.svelte";
</script>

<nav class="hosts" aria-label="Docker hosts">
  {#each app.hosts as h (h.name)}
    <button
      class:active={h.name === app.activeHost}
      title={`${h.name} · ${h.url}${h.error ? `\n${h.error}` : ""}`}
      aria-pressed={h.name === app.activeHost}
      data-testid="host-tab"
      data-host={h.name}
      onclick={() => {
        location.hash = `#/${encodeURIComponent(h.name)}`;
      }}
    >
      <span class="dot {h.status}" aria-label={h.status}></span>
      {h.displayName}
    </button>
  {/each}
</nav>

<style>
  .hosts {
    display: flex;
    gap: 4px;
    overflow-x: auto;
  }
  button {
    display: flex;
    align-items: center;
    gap: 6px;
    white-space: nowrap;
    background: transparent;
    border: 1px solid var(--border);
    color: var(--fg-muted);
    padding: 4px 10px;
    border-radius: 6px;
  }
  button.active {
    background: var(--bg-raised);
    color: var(--fg);
    border-color: var(--accent);
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--fg-muted);
  }
  .dot.connected {
    background: var(--ok);
  }
  .dot.error {
    background: var(--err);
  }
  .dot.connecting {
    background: var(--warn);
  }
</style>
