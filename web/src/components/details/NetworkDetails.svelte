<script lang="ts">
  import NodeLink from "./NodeLink.svelte";
  import type { NetworkDetails } from "../../lib/types";

  let { data }: { data: NetworkDetails } = $props();

  const reach = $derived.by(() => {
    if (data.name === "host") return "Members share the host's network stack.";
    if (data.ingress) return "Swarm routing mesh: members do not reach each other through it.";
    if (!data.icc) return "Inter-container communication is disabled: members cannot reach each other.";
    if (!data.dns) return "Members can reach each other by IP only (default bridge, no DNS).";
    return "Members can reach each other by name (embedded DNS).";
  });
</script>

<p class="note" data-testid="network-reach">{reach}</p>
<dl class="kv">
  <dt>Driver</dt>
  <dd>{data.driver} · {data.scope}</dd>
  <dt>Flags</dt>
  <dd>
    {[data.internal && "internal (no egress)", data.attachable && "attachable", data.ipv6 && "IPv6", data.reachGroup && "large: shown as hub"]
      .filter(Boolean)
      .join(", ") || "none"}
  </dd>
  <dt>Subnets</dt>
  <dd>
    {#each data.subnets as s, i (i)}<div><code>{s.subnet}</code>{s.gateway ? ` via ${s.gateway}` : ""}</div>{:else}none{/each}
  </dd>
  {#if data.labels && Object.keys(data.labels).length}
    <dt>Labels</dt>
    <dd>{#each Object.entries(data.labels).sort() as [k, v] (k)}<div><code>{k}</code>={v}</div>{/each}</dd>
  {/if}
</dl>
<h3>Members <span class="muted">{data.members.length}</span></h3>
<ul data-testid="network-members">
  {#each data.members as m (m.id)}
    <li><NodeLink id={m.id} label={m.name} /> {#if m.ipv4}<code>{m.ipv4}</code>{/if}</li>
  {:else}
    <li class="muted">no local members</li>
  {/each}
</ul>

<style>
  .kv {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 4px 12px;
    margin: 0;
  }
  dt,
  .muted {
    color: var(--fg-muted);
  }
  dd {
    margin: 0;
  }
  .note {
    padding: 6px 8px;
    background: var(--bg-raised);
    border-left: 3px solid var(--accent);
  }
  ul {
    padding-left: 14px;
  }
</style>
