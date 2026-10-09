<script lang="ts">
  import { app } from "../lib/state.svelte";
</script>

<section class="legend">
  <h2>Legend</h2>
  {#if app.mode === "reachability"}
    <dl>
      <dt><span class="line thick rainbow"></span></dt>
      <dd>Can reach each other by name (shared network, embedded DNS); color = network</dd>
      <dt><span class="line thin"></span></dt>
      <dd>IP only: default <code>bridge</code>, no DNS</dd>
      <dt><span class="line orange"></span></dt>
      <dd>Shares a network namespace, uses the host network, or publishes ports</dd>
      <dt><span class="ring"></span></dt>
      <dd>Isolated (<code>network_mode: none</code>)</dd>
    </dl>
    <p>Networks with ICC disabled and the swarm ingress network create no peer links. Networks with more than 50 members are shown as a hub.</p>
  {:else}
    <dl>
      <dt><span class="dot" style:background="#3fb950"></span></dt>
      <dd>Running / healthy</dd>
      <dt><span class="dot" style:background="#d29922"></span></dt>
      <dd>Starting / restarting</dd>
      <dt><span class="dot" style:background="#f85149"></span></dt>
      <dd>Unhealthy / dead</dd>
      <dt><span class="dot" style:background="#6e7681"></span></dt>
      <dd>Stopped</dd>
    </dl>
  {/if}
</section>

<style>
  dl {
    display: grid;
    grid-template-columns: 28px 1fr;
    gap: 4px 8px;
    margin: 0;
    align-items: center;
  }
  dd {
    margin: 0;
    font-size: 12px;
  }
  p {
    font-size: 11px;
    color: var(--fg-muted);
  }
  .line {
    display: block;
    height: 2px;
    background: #8b949e;
  }
  .line.thick {
    height: 4px;
  }
  .line.rainbow {
    background: linear-gradient(90deg, #ff7b72, #e3b341, #3fb950, #58a6ff, #d2a8ff);
  }
  .line.thin {
    height: 1px;
  }
  .line.orange {
    height: 3px;
    background: #f0883e;
  }
  .ring {
    display: block;
    width: 14px;
    height: 14px;
    border: 2px solid #8b949e;
    border-radius: 50%;
  }
  .dot {
    display: block;
    width: 12px;
    height: 12px;
    border-radius: 50%;
  }
</style>
