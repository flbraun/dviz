<script lang="ts">
  import { hostPath, subscribe } from "../../lib/api";
  import { formatBytes } from "../../lib/style";
  import type { StatsSample } from "../../lib/types";

  let { host, id, running }: { host: string; id: string; running: boolean } = $props();

  const MAX = 60;
  let samples = $state<StatsSample[]>([]);
  let error = $state<string | null>(null);

  $effect(() => {
    samples = [];
    error = null;
    if (!running) return;
    return subscribe(`${hostPath(host)}/containers/${encodeURIComponent(id)}/stats`, {
      sample: (s) => {
        samples = [...samples.slice(-(MAX - 1)), s as StatsSample];
      },
      error: (e) => {
        error = (e as { error: string }).error;
      },
    });
  });

  const last = $derived(samples.at(-1));
  const prev = $derived(samples.at(-2));
  const rate = (a: number, b: number, dt: number) => (dt > 0 ? Math.max(0, (a - b) / dt) : 0);
  const dt = $derived(last && prev ? (last.ts - prev.ts) / 1000 : 0);

  function spark(values: number[], max?: number): string {
    if (values.length < 2) return "";
    const top = max ?? Math.max(...values, 1e-9);
    // Newest sample at the right edge; the line grows in from the right.
    const offset = MAX - values.length;
    return values.map((v, i) => `${((offset + i) / (MAX - 1)) * 100},${30 - (Math.min(v, top) / top) * 28}`).join(" ");
  }
</script>

{#if !running}
  <p class="muted">Container is not running.</p>
{:else if error}
  <p class="err">{error}</p>
{:else if !last}
  <p class="muted">Waiting for samples…</p>
{:else}
  <div class="grid" data-testid="stats">
    <div class="metric">
      <div class="label">CPU</div>
      <div class="value" data-testid="stats-cpu">{last.cpuPct.toFixed(1)} %</div>
      <svg viewBox="0 0 100 30" preserveAspectRatio="none"><polyline points={spark(samples.map((s) => s.cpuPct))} /></svg>
    </div>
    <div class="metric">
      <div class="label">Memory</div>
      <div class="value" data-testid="stats-mem">{formatBytes(last.memUsage)} <span class="muted">/ {formatBytes(last.memLimit)}</span></div>
      <svg viewBox="0 0 100 30" preserveAspectRatio="none"><polyline points={spark(samples.map((s) => s.memUsage))} /></svg>
    </div>
    <div class="metric">
      <div class="label">Network</div>
      <div class="value">↓ {formatBytes(rate(last.netRx, prev?.netRx ?? last.netRx, dt))}/s · ↑ {formatBytes(rate(last.netTx, prev?.netTx ?? last.netTx, dt))}/s</div>
      <div class="muted">total ↓ {formatBytes(last.netRx)} · ↑ {formatBytes(last.netTx)}</div>
    </div>
    <div class="metric">
      <div class="label">Block I/O</div>
      <div class="value">read {formatBytes(last.blkRead)} · write {formatBytes(last.blkWrite)}</div>
    </div>
    <div class="metric">
      <div class="label">PIDs</div>
      <div class="value">{last.pids}</div>
    </div>
  </div>
{/if}

<style>
  .grid {
    display: grid;
    gap: 10px;
  }
  .label {
    color: var(--fg-muted);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .value {
    font-variant-numeric: tabular-nums;
  }
  svg {
    width: 100%;
    height: 34px;
    background: var(--bg-raised);
    border-radius: 4px;
  }
  polyline {
    fill: none;
    stroke: var(--accent);
    stroke-width: 1.5;
    vector-effect: non-scaling-stroke;
  }
  .muted {
    color: var(--fg-muted);
  }
  .err {
    color: var(--err);
  }
</style>
