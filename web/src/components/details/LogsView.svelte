<script lang="ts">
  import { tick } from "svelte";
  import { hostPath, subscribe } from "../../lib/api";
  import type { LogLine } from "../../lib/types";

  let { host, id }: { host: string; id: string } = $props();

  const MAX_LINES = 2000;
  let lines = $state<LogLine[]>([]);
  let follow = $state(true);
  let ended = $state(false);
  let error = $state<string | null>(null);
  let box: HTMLPreElement | undefined = $state();

  $effect(() => {
    lines = [];
    ended = false;
    error = null;
    const f = follow;
    return subscribe(`${hostPath(host)}/containers/${encodeURIComponent(id)}/logs?tail=200&follow=${f ? 1 : 0}`, {
      line: (l) => {
        lines = lines.length >= MAX_LINES ? [...lines.slice(1), l as LogLine] : [...lines, l as LogLine];
        if (f) void tick().then(() => box?.scrollTo({ top: box.scrollHeight }));
      },
      error: (e) => {
        error = (e as { error: string }).error;
      },
      eof: () => {
        ended = true;
      },
    });
  });
</script>

<div class="bar">
  <label><input type="checkbox" bind:checked={follow} /> Follow</label>
  <span class="muted">{lines.length} lines{ended && !follow ? "" : ended ? " · stream ended" : ""}</span>
</div>
{#if error}<p class="err">{error}</p>{/if}
<pre bind:this={box} data-testid="logs">{#each lines as l, i (i)}<span class={l.stream} title={l.ts}>{l.text}
</span>{/each}</pre>

<style>
  .bar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 6px;
  }
  pre {
    margin: 0;
    height: 55vh;
    overflow: auto;
    background: #010409;
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 6px;
    font-size: 11px;
    line-height: 1.4;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .stderr {
    color: #ffa198;
  }
  .muted {
    color: var(--fg-muted);
    font-size: 12px;
  }
  .err {
    color: var(--err);
  }
</style>
