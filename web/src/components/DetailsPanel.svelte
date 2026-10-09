<script lang="ts">
  import { fetchEntity } from "../lib/api";
  import { app } from "../lib/state.svelte";
  import { KIND_LABEL, nodeColor } from "../lib/style";
  import type { ContainerDetails as CD, Entity, NetworkDetails as ND } from "../lib/types";
  import ContainerDetails from "./details/ContainerDetails.svelte";
  import DataView from "./details/DataView.svelte";
  import NetworkDetails from "./details/NetworkDetails.svelte";
  import Related from "./details/Related.svelte";

  let entity = $state<Entity | null>(null);
  let error = $state<string | null>(null);

  // Store nodes are mutated in place to keep their layout; copy so changes propagate.
  const node = $derived.by(() => {
    void app.graph?.version;
    const n = app.selectedId ? app.graph?.nodes.get(app.selectedId) : undefined;
    return n ? { ...n } : null;
  });
  // Refetch when the selection or its live state changes.
  const refreshKey = $derived(node ? `${node.id}|${node.status}|${JSON.stringify(node.attrs ?? {})}` : null);

  $effect(() => {
    const key = refreshKey;
    const host = app.activeHost;
    const id = app.selectedId;
    if (!key || !host || !id) {
      entity = null;
      return;
    }
    const ctl = new AbortController();
    fetchEntity(host, id, ctl.signal)
      .then((e) => {
        if (e.node.id !== entity?.node.id) entity = null;
        entity = e;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      });
    return () => ctl.abort();
  });

  // The node in the store is live; the fetched entity may lag behind by one request.
  const shown = $derived(entity && node && entity.node.id === node.id ? entity : null);
</script>

{#if node}
  <aside class="panel" data-testid="details" data-node-id={node.id}>
    <header>
      <span class="swatch" style:background={nodeColor(node)}></span>
      <div class="title">
        <h2 data-testid="details-name">{node.kind === "host" ? (app.active?.displayName ?? node.name) : node.name}</h2>
        <div class="sub">
          {KIND_LABEL[node.kind]}{#if node.status}{" · "}<span data-testid="details-status">{node.status}</span>{/if}
        </div>
      </div>
      <button class="close" aria-label="Close details" onclick={() => app.select(null)}>×</button>
    </header>
    <div class="body">
      {#if node.attrs?.["error"]}<p class="err">{node.attrs["error"]}</p>{/if}
      {#if error}<p class="err">{error}</p>{/if}
      {#if shown}
        {#if node.kind === "container" && shown.data}
          <ContainerDetails host={app.activeHost!} data={shown.data as CD} />
        {:else if node.kind === "network" && shown.data}
          <NetworkDetails data={shown.data as ND} />
        {:else if shown.data}
          <DataView data={shown.data} />
        {/if}
        <Related related={shown.related} />
      {:else if !error}
        <p class="muted">Loading…</p>
      {/if}
    </div>
  </aside>
{/if}

<style>
  .panel {
    flex: none;
    width: min(440px, 100%);
    background: var(--bg-panel);
    border-left: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    z-index: 2;
  }
  header {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border);
  }
  .swatch {
    margin-top: 6px;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    flex: none;
  }
  .title {
    flex: 1;
    min-width: 0;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    text-transform: none;
    letter-spacing: normal;
    color: var(--fg);
    overflow-wrap: anywhere;
  }
  .sub {
    color: var(--fg-muted);
    font-size: 12px;
  }
  .close {
    background: none;
    border: 0;
    color: var(--fg-muted);
    font-size: 20px;
    line-height: 1;
  }
  .body {
    padding: 12px 14px;
    overflow-y: auto;
    flex: 1;
  }
  .muted {
    color: var(--fg-muted);
  }
  .err {
    color: var(--err);
  }
</style>
