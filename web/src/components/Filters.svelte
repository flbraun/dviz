<script lang="ts">
  import { ALL_KINDS, app, REACH_KINDS } from "../lib/state.svelte";
  import { KIND_COLOR, KIND_LABEL } from "../lib/style";

  const counts = $derived.by(() => {
    void app.graph?.version;
    const out: Record<string, number> = {};
    for (const n of app.graph?.nodes.values() ?? []) out[n.kind] = (out[n.kind] ?? 0) + 1;
    return out;
  });
</script>

<section>
  <h2>Show</h2>
  <ul>
    {#each ALL_KINDS as kind (kind)}
      {#if counts[kind]}
        <li>
          <label class:inactive={app.mode === "reachability" && !REACH_KINDS.has(kind)}>
            <input
              type="checkbox"
              checked={!app.hiddenKinds.includes(kind)}
              onchange={() => app.toggleKind(kind)}
              data-testid="kind-filter"
              data-kind={kind}
            />
            <span class="swatch" style:background={KIND_COLOR[kind]}></span>
            {KIND_LABEL[kind]}
            <span class="count">{counts[kind]}</span>
          </label>
        </li>
      {/if}
    {/each}
  </ul>
  <label>
    <input type="checkbox" bind:checked={app.hideStopped} />
    Hide stopped containers &amp; tasks
  </label>
</section>

<style>
  ul {
    list-style: none;
    padding: 0;
    margin: 0 0 8px;
  }
  label {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 0;
  }
  label.inactive {
    opacity: 0.45;
  }
  .swatch {
    width: 10px;
    height: 10px;
    border-radius: 2px;
  }
  .count {
    margin-left: auto;
    color: var(--fg-muted);
    font-size: 11px;
  }
</style>
