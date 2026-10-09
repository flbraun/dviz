<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { isStopped, KIND_LABEL, nodeColor } from "../lib/style";
  import type { GNode } from "../lib/graphStore.svelte";

  const LIMIT = 100;

  const results = $derived.by((): GNode[] => {
    void app.graph?.version;
    const q = app.query.trim().toLowerCase();
    const store = app.graph;
    if (!q || !store) return [];
    const hidden = new Set(app.hiddenKinds);
    return [...store.nodes.values()]
      .filter((n) => !hidden.has(n.kind) && !(app.hideStopped && isStopped(n)))
      .filter((n) => n.name.toLowerCase().includes(q) || n.id.toLowerCase().includes(q))
      .sort((a, b) => a.name.localeCompare(b.name))
      .slice(0, LIMIT);
  });
</script>

<section>
  <input type="search" placeholder="Search entities…" aria-label="Search entities" bind:value={app.query} />
  {#if app.query.trim()}
    <ul class="results" data-testid="search-results">
      {#each results as n (n.id)}
        <li>
          <button
            class:selected={n.id === app.selectedId}
            data-testid="search-result"
            data-node-id={n.id}
            onclick={() => app.select(n.id, true)}
          >
            <span class="swatch" style:background={nodeColor(n)}></span>
            <span class="name">{n.name}</span>
            <span class="meta">{KIND_LABEL[n.kind]}{n.status ? ` · ${n.status}` : ""}</span>
          </button>
        </li>
      {:else}
        <li class="empty">No matches</li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  input {
    width: 100%;
  }
  .results {
    list-style: none;
    margin: 6px 0 0;
    padding: 0;
    max-height: 40vh;
    overflow-y: auto;
  }
  button {
    display: grid;
    grid-template-columns: 10px 1fr;
    column-gap: 8px;
    width: 100%;
    text-align: left;
    background: transparent;
    border: 0;
    padding: 4px 6px;
    border-radius: 4px;
    color: var(--fg);
  }
  button:hover,
  button.selected {
    background: var(--bg-raised);
  }
  .swatch {
    grid-row: span 2;
    align-self: center;
    width: 10px;
    height: 10px;
    border-radius: 50%;
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta {
    font-size: 11px;
    color: var(--fg-muted);
  }
  .empty {
    color: var(--fg-muted);
    padding: 4px 6px;
  }
</style>
