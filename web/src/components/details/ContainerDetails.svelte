<script lang="ts">
  import LogsView from "./LogsView.svelte";
  import NodeLink from "./NodeLink.svelte";
  import StatsView from "./StatsView.svelte";
  import { formatBytes } from "../../lib/style";
  import type { ContainerDetails } from "../../lib/types";

  let { host, data }: { host: string; data: ContainerDetails } = $props();

  type Tab = "summary" | "network" | "stats" | "logs";
  let tab = $state<Tab>("summary");
  const tabs: { id: Tab; label: string }[] = [
    { id: "summary", label: "Summary" },
    { id: "network", label: "Network" },
    { id: "stats", label: "Stats" },
    { id: "logs", label: "Logs" },
  ];

  const mode = $derived.by(() => {
    const m = data.networkMode;
    if (m === "host") return "Uses the host's network stack";
    if (m === "none") return "Isolated: no network";
    if (m.startsWith("container:")) return "Shares another container's network namespace";
    return null;
  });
</script>

<div class="tabs" role="tablist">
  {#each tabs as t (t.id)}
    <button role="tab" aria-selected={tab === t.id} class:active={tab === t.id} onclick={() => (tab = t.id)}>{t.label}</button>
  {/each}
</div>

{#if tab === "summary"}
  <dl class="kv" data-testid="container-summary">
    <dt>Image</dt>
    <dd data-testid="container-image">{data.image}</dd>
    <dt>Command</dt>
    <dd><code>{data.command.join(" ")}</code></dd>
    <dt>State</dt>
    <dd>
      {data.state.status}{data.state.health ? ` (${data.state.health})` : ""}{!data.state.running && data.state.finishedAt && !data.state.finishedAt.startsWith("0001")
        ? ` · exit ${data.state.exitCode}`
        : ""}{data.state.oomKilled ? " · OOM killed" : ""}
    </dd>
    {#if data.state.error}<dt>Error</dt><dd>{data.state.error}</dd>{/if}
    <dt>Started</dt>
    <dd>{data.state.startedAt.startsWith("0001") ? "never" : new Date(data.state.startedAt).toLocaleString()}</dd>
    <dt>Restart</dt>
    <dd>{data.restartPolicy || "no"} · {data.restartCount} restarts</dd>
    {#if data.project}<dt>Compose</dt><dd>{data.project} / {data.service}</dd>{/if}
    <dt>Ports</dt>
    <dd data-testid="container-ports">
      {#each data.ports as p, i (i)}
        <div>{p.hostPort ? `${p.hostIp ? `${p.hostIp}:` : ""}${p.hostPort} → ` : ""}{p.container}</div>
      {:else}
        none
      {/each}
    </dd>
    <dt>Mounts</dt>
    <dd>
      {#each data.mounts as m, i (i)}
        <div><span class="muted">{m.type}</span> {m.name || m.source} → {m.destination}{m.rw ? "" : " (ro)"}</div>
      {:else}
        none
      {/each}
    </dd>
    <dt>Env</dt>
    <dd data-testid="container-env">
      {#each data.env as key (key)}
        <div><code>{key}</code> <span class="muted">= ••••</span></div>
      {:else}
        none
      {/each}
    </dd>
    <dt>Resources</dt>
    <dd>
      memory {data.resources.memory ? formatBytes(data.resources.memory) : "unlimited"} · cpus
      {data.resources.nanoCpus ? (data.resources.nanoCpus / 1e9).toFixed(2) : "unlimited"}
    </dd>
    <dt>User</dt>
    <dd>{data.user || "default"}</dd>
    {#if Object.keys(data.labels).length}
      <dt>Labels</dt>
      <dd>
        {#each Object.entries(data.labels).sort() as [k, v] (k)}
          <div><code>{k}</code>={v}</div>
        {/each}
      </dd>
    {/if}
  </dl>
{:else if tab === "network"}
  <section data-testid="container-network">
    {#if mode}<p class="note">{mode}</p>{/if}
    {#if data.netnsOwner}
      <p>Network namespace of <NodeLink id={data.netnsOwner.id} label={data.netnsOwner.name} /> (inherits its peers)</p>
    {/if}
    {#if data.netnsSharers?.length}
      <p>
        Shared with:
        {#each data.netnsSharers as s, i (s.id)}{i ? ", " : ""}<NodeLink id={s.id} label={s.name} />{/each}
      </p>
    {/if}

    <h3>Can reach <span class="muted">{data.peers.length}</span></h3>
    {#if data.peers.length}
      <ul class="peers" data-testid="peer-list">
        {#each data.peers as p (p.id)}
          <li data-testid="peer" data-node-id={p.id}>
            <NodeLink id={p.id} label={p.name} /> <span class="muted">{p.state}</span>
            <ul>
              {#each p.networks as n (n.network)}
                <li>
                  <span class="net">{n.network}</span>
                  {#if n.dns && n.names?.length}
                    <span data-testid="peer-dns">{n.names.join(", ")}</span>
                  {:else}
                    <span class="muted">no DNS</span>
                  {/if}
                  {#if n.ip}<code>{n.ip}</code>{/if}
                </li>
              {/each}
            </ul>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted">No other container shares a network with this one.</p>
    {/if}

    <h3>Endpoints</h3>
    {#each data.networks as n (n.network)}
      <div class="endpoint">
        <NodeLink id={n.nodeId} label={n.network} />
        {#if n.ip}<code>{n.ip}</code>{/if}
        {#if n.ipv6}<code>{n.ipv6}</code>{/if}
        {#if n.dnsNames?.length}<div class="muted">DNS: {n.dnsNames.join(", ")}</div>{/if}
      </div>
    {:else}
      <p class="muted">none</p>
    {/each}

    <h3>Published ports</h3>
    {#each data.ports.filter((p) => p.hostPort) as p, i (i)}
      <div><code>{p.hostIp || "*"}:{p.hostPort}</code> → {p.container}</div>
    {:else}
      <p class="muted">none — not reachable from outside the host</p>
    {/each}
  </section>
{:else if tab === "stats"}
  <StatsView {host} id={data.id} running={data.state.running} />
{:else}
  <LogsView {host} id={data.id} />
{/if}

<style>
  .tabs {
    display: flex;
    gap: 2px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 10px;
  }
  .tabs button {
    background: none;
    border: 0;
    border-bottom: 2px solid transparent;
    color: var(--fg-muted);
    padding: 6px 10px;
  }
  .tabs button.active {
    color: var(--fg);
    border-bottom-color: var(--accent);
  }
  .kv {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 4px 12px;
    margin: 0;
  }
  dt {
    color: var(--fg-muted);
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .muted {
    color: var(--fg-muted);
  }
  .note {
    padding: 6px 8px;
    background: var(--bg-raised);
    border-left: 3px solid #f0883e;
  }
  .peers {
    list-style: none;
    padding: 0;
  }
  .peers > li {
    margin-bottom: 6px;
  }
  .peers ul {
    padding-left: 14px;
    margin: 2px 0 0;
    font-size: 12px;
  }
  .net {
    font-weight: 600;
    margin-right: 4px;
  }
  .endpoint {
    margin-bottom: 6px;
  }
  code {
    font-size: 12px;
  }
</style>
