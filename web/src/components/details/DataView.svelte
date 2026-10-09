<script lang="ts">
  import DataView from "./DataView.svelte";
  import NodeLink from "./NodeLink.svelte";

  let { data }: { data: unknown } = $props();

  function label(key: string): string {
    return key.replace(/([a-z])([A-Z])/g, "$1 $2").replace(/^./, (c) => c.toUpperCase());
  }

  function isEmpty(v: unknown): boolean {
    if (v === null || v === undefined || v === "") return true;
    if (Array.isArray(v)) return v.length === 0;
    if (typeof v === "object") return Object.keys(v).length === 0;
    return false;
  }

  function isScalar(v: unknown): v is string | number | boolean {
    return typeof v === "string" || typeof v === "number" || typeof v === "boolean";
  }

  const entries = $derived(
    data && typeof data === "object" && !Array.isArray(data)
      ? Object.entries(data as Record<string, unknown>).filter(([k, v]) => !isEmpty(v) && k !== "nodeId")
      : [],
  );
  const nodeId = $derived(
    data && typeof data === "object" && "nodeId" in data ? String((data as { nodeId: unknown }).nodeId) : null,
  );
</script>

{#if isScalar(data)}
  <span class="v">{String(data)}</span>
{:else if Array.isArray(data)}
  {#if data.every(isScalar)}
    <span class="v">{data.join(", ")}</span>
  {:else}
    <ul>
      {#each data as item, i (i)}
        <li><DataView data={item} /></li>
      {/each}
    </ul>
  {/if}
{:else}
  <dl>
    {#each entries as [k, v] (k)}
      <dt>{label(k)}</dt>
      <dd>
        {#if nodeId && (k === "name" || k === "network")}
          <NodeLink id={nodeId} label={String(v)} />
        {:else}
          <DataView data={v} />
        {/if}
      </dd>
    {/each}
  </dl>
{/if}

<style>
  dl {
    display: grid;
    grid-template-columns: minmax(80px, max-content) 1fr;
    gap: 2px 10px;
    margin: 0;
  }
  dt {
    color: var(--fg-muted);
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  ul {
    margin: 0;
    padding-left: 14px;
  }
</style>
