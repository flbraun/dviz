<script lang="ts">
  import NodeLink from "./NodeLink.svelte";
  import { KIND_LABEL } from "../../lib/style";
  import type { Related } from "../../lib/types";

  let { related }: { related: Related[] } = $props();

  const LINK_LABEL: Record<string, string> = {
    host: "On host",
    network: "Networks",
    mount: "Mounts",
    image: "Image",
    project: "Project",
    task: "Tasks",
    runs: "Container",
    placed: "Placed on",
    secret: "Secrets",
    config: "Configs",
    reach: "Can reach",
    netns: "Shares network namespace",
    hostnet: "Host network",
    exposed: "Published to host",
  };

  const groups = $derived.by(() => {
    const m = new Map<string, Related[]>();
    for (const r of related) {
      if (r.kind === "host" && r.link === "host") continue;
      const key = r.link === "host" ? `${r.link}:${r.kind}` : r.link;
      m.set(key, [...(m.get(key) ?? []), r]);
    }
    return [...m.entries()].map(([key, items]) => {
      const [link, kind] = key.split(":");
      const title = link === "host" && kind ? KIND_LABEL[kind as keyof typeof KIND_LABEL] : (LINK_LABEL[link!] ?? link);
      return { key, title, items: items.sort((a, b) => a.name.localeCompare(b.name)) };
    });
  });
</script>

{#if groups.length}
  <section>
    <h3>Related</h3>
    {#each groups as g (g.key)}
      <h4>{g.title} <span class="count">{g.items.length}</span></h4>
      <ul>
        {#each g.items.slice(0, 200) as r (r.id)}
          <li><NodeLink id={r.id} label={r.name || r.id} />{#if r.status}<span class="status">{" · "}{r.status}</span>{/if}</li>
        {/each}
      </ul>
    {/each}
  </section>
{/if}

<style>
  h4 {
    margin: 8px 0 2px;
    font-size: 12px;
    color: var(--fg-muted);
    font-weight: 600;
  }
  ul {
    margin: 0;
    padding-left: 14px;
  }
  .count,
  .status {
    color: var(--fg-muted);
    font-weight: normal;
  }
</style>
