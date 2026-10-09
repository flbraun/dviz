<script lang="ts">
  import { onMount } from "svelte";
  import DetailsPanel from "./components/DetailsPanel.svelte";
  import Graph3D from "./components/Graph3D.svelte";
  import HostSwitcher from "./components/HostSwitcher.svelte";
  import Sidebar from "./components/Sidebar.svelte";
  import { app } from "./lib/state.svelte";

  onMount(() => app.start());
</script>

<div class="app">
  <header class="top">
    <h1>dviz</h1>
    <HostSwitcher />
    <div class="modes" role="radiogroup" aria-label="View mode">
      <button role="radio" aria-checked={app.mode === "topology"} class:active={app.mode === "topology"} data-testid="mode-topology" onclick={() => (app.mode = "topology")}>Topology</button>
      <button role="radio" aria-checked={app.mode === "reachability"} class:active={app.mode === "reachability"} data-testid="mode-reachability" onclick={() => (app.mode = "reachability")}>Reachability</button>
    </div>
  </header>
  <div class="main">
    <Sidebar />
    <div class="stage">
      <div class="canvas">
        <Graph3D />
        {#if app.hostsLoaded && !app.hosts.length}
          <p class="overlay">No hosts configured.</p>
        {:else if app.active?.status === "error"}
          <p class="overlay err">Cannot connect to {app.active.displayName}: {app.active.error}</p>
        {:else if app.active?.status === "connecting" || (app.graph && !app.graph.loaded)}
          <p class="overlay">Connecting…</p>
        {/if}
      </div>
      <DetailsPanel />
    </div>
  </div>
</div>

<style>
  .app {
    display: flex;
    flex-direction: column;
    height: 100vh;
  }
  .top {
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-panel);
    min-width: 0;
  }
  h1 {
    margin: 0;
    font-size: 16px;
    letter-spacing: 0.04em;
  }
  .modes {
    margin-left: auto;
    display: flex;
    border: 1px solid var(--border);
    border-radius: 6px;
    overflow: hidden;
    flex: none;
  }
  .modes button {
    background: transparent;
    border: 0;
    color: var(--fg-muted);
    padding: 4px 10px;
  }
  .modes button.active {
    background: var(--accent);
    color: #0d1117;
  }
  .main {
    flex: 1;
    display: flex;
    min-height: 0;
  }
  .stage {
    flex: 1;
    min-width: 0;
    display: flex;
  }
  .canvas {
    position: relative;
    flex: 1;
    min-width: 0;
  }
  .overlay {
    position: absolute;
    top: 12px;
    left: 50%;
    transform: translateX(-50%);
    background: var(--bg-panel);
    border: 1px solid var(--border);
    padding: 6px 12px;
    border-radius: 6px;
    z-index: 1;
  }
  .err {
    color: var(--err);
  }
  @media (max-width: 700px) {
    .main {
      flex-direction: column;
    }
    .top {
      flex-wrap: wrap;
    }
  }
</style>
