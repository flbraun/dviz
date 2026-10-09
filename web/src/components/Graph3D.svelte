<script lang="ts">
  import ForceGraph3D, { type ForceGraph3DInstance } from "3d-force-graph";
  import * as THREE from "three";
  import SpriteText from "three-spritetext";
  import { onDestroy, onMount } from "svelte";
  import { app, REACH_KINDS } from "../lib/state.svelte";
  import type { GLink, GNode } from "../lib/graphStore.svelte";
  import { isStopped, LINK_COLOR, networkColor, nodeColor } from "../lib/style";
  import type { Kind } from "../lib/types";

  /** Links drawn in each view mode. */
  const TOPOLOGY_LINKS = new Set(["host", "network", "mount", "image", "project", "task", "runs", "placed", "secret", "config", "hostnet", "netns"]);
  const REACH_LINKS = new Set(["host", "project", "reach", "netns", "hostnet", "exposed"]);
  const PEER_LINKS: ReadonlySet<string> = new Set(["reach", "netns"]);
  const DIM_COLOR = "#30363d";

  type NodeObj = { group: THREE.Group; mat: THREE.MeshLambertMaterial; label: SpriteText; ring?: THREE.Mesh };

  let el: HTMLDivElement;
  let fg: ForceGraph3DInstance<GNode, GLink> | null = null;
  let hovered: string | null = null;
  let highlight: Set<string> | null = null;
  let visibleNodes = $state(0);
  // Three.js objects are cached per node object, so they survive data updates and host switches.
  const objects = new WeakMap<GNode, NodeObj>();
  let resize: ResizeObserver | null = null;

  function geometry(kind: Kind): THREE.BufferGeometry {
    switch (kind) {
      case "host": return new THREE.IcosahedronGeometry(10);
      case "container": return new THREE.SphereGeometry(4, 20, 14);
      case "network": return new THREE.OctahedronGeometry(6);
      case "hostnet": return new THREE.OctahedronGeometry(7, 1);
      case "volume": return new THREE.CylinderGeometry(3, 3, 5, 16);
      case "bind": return new THREE.CylinderGeometry(2, 2, 4, 6);
      case "image": return new THREE.BoxGeometry(5, 5, 5);
      case "project": return new THREE.TorusGeometry(6, 1.6, 10, 28);
      case "service": return new THREE.DodecahedronGeometry(5.5);
      case "task": return new THREE.TetrahedronGeometry(3);
      case "node": return new THREE.ConeGeometry(5, 8, 6);
      case "secret": return new THREE.OctahedronGeometry(3);
      case "config": return new THREE.BoxGeometry(3, 4, 1);
    }
  }

  function displayName(n: GNode): string {
    if (n.kind === "host") return app.active?.displayName ?? n.name;
    return n.name;
  }

  const MAX_LABEL = 25;

  /** Label text, ellipsized so long unique identifiers stay readable; hover shows the full name. */
  function labelText(n: GNode): string {
    const name = displayName(n);
    return name.length > MAX_LABEL ? `${name.slice(0, MAX_LABEL)}…` : name;
  }

  function nodeObject(n: GNode): THREE.Object3D {
    let o = objects.get(n);
    if (!o) {
      const mat = new THREE.MeshLambertMaterial({ color: nodeColor(n), transparent: true, opacity: 0.95 });
      const group = new THREE.Group();
      group.add(new THREE.Mesh(geometry(n.kind), mat));
      const label = new SpriteText(labelText(n), 3, "#e6edf3");
      label.backgroundColor = "rgba(13,17,23,0.7)";
      label.padding = 1;
      label.position.y = n.kind === "host" ? 15 : 8;
      group.add(label);
      o = { group, mat, label };
      objects.set(n, o);
    }
    paint(n, o);
    return o.group;
  }

  function paint(n: GNode, o: NodeObj): void {
    const lit = !highlight || highlight.has(n.id);
    o.mat.color.set(nodeColor(n));
    o.mat.opacity = lit ? 0.95 : 0.12;
    // Setting text re-renders the sprite's canvas, so only do it on change.
    const text = labelText(n);
    if (o.label.text !== text) o.label.text = text;
    o.label.material.opacity = lit || n.id === hovered ? 1 : 0.15;
    const isolated = n.attrs?.["isolated"] === "true";
    if (isolated && !o.ring) {
      o.ring = new THREE.Mesh(new THREE.TorusGeometry(6, 0.5, 8, 32), new THREE.MeshBasicMaterial({ color: "#8b949e" }));
      o.group.add(o.ring);
    }
    if (o.ring) o.ring.visible = isolated;
  }

  function repaint(): void {
    const store = app.graph;
    if (!store) return;
    for (const n of store.nodes.values()) {
      const o = objects.get(n);
      if (o) paint(n, o);
    }
    // Re-setting accessors makes the graph re-evaluate link styles.
    fg?.linkColor(fg.linkColor()).linkWidth(fg.linkWidth()).linkDirectionalParticles(fg.linkDirectionalParticles());
  }

  function computeHighlight(): Set<string> | null {
    const store = app.graph;
    const sel = app.selectedId;
    if (!store || !sel || !store.nodes.has(sel)) return null;
    const allowed = app.mode === "topology" ? TOPOLOGY_LINKS : REACH_LINKS;
    const out = new Set([sel]);
    for (const l of store.links.values()) {
      if (!allowed.has(l.kind)) continue;
      if (l.sourceId === sel) out.add(l.targetId);
      if (l.targetId === sel) out.add(l.sourceId);
    }
    // Every peer the selection can reach is highlighted, whatever the mode.
    for (const id of store.neighbors(sel, PEER_LINKS)) out.add(id);
    return out;
  }

  function visibleData(): { nodes: GNode[]; links: GLink[] } {
    const store = app.graph;
    if (!store) return { nodes: [], links: [] };
    const hidden = new Set(app.hiddenKinds);
    const reach = app.mode === "reachability";
    const nodes = [...store.nodes.values()].filter((n) => {
      if (hidden.has(n.kind)) return false;
      if (app.hideStopped && isStopped(n)) return false;
      if (reach && !REACH_KINDS.has(n.kind) && !(n.kind === "network" && n.attrs?.["reachGroup"] === "true")) return false;
      return true;
    });
    const ids = new Set(nodes.map((n) => n.id));
    const allowed = reach ? REACH_LINKS : TOPOLOGY_LINKS;
    const links = [...store.links.values()].filter((l) => {
      if (!ids.has(l.sourceId) || !ids.has(l.targetId)) return false;
      // Large networks draw membership instead of pairwise reach links.
      if (reach && l.kind === "network") return store.nodes.get(l.targetId)?.attrs?.["reachGroup"] === "true";
      return allowed.has(l.kind);
    });
    return { nodes, links };
  }

  function linkLit(l: GLink): boolean {
    return !highlight || (highlight.has(l.sourceId) && highlight.has(l.targetId) && (l.sourceId === app.selectedId || l.targetId === app.selectedId));
  }

  function linkColor(l: GLink): string {
    if (!linkLit(l)) return DIM_COLOR;
    if (l.kind === "reach") return networkColor(l.networks?.[0] ?? "");
    return LINK_COLOR[l.kind] ?? "#484f58";
  }

  function linkWidth(l: GLink): number {
    switch (l.kind) {
      case "reach": return l.dns ? 1.2 : 0.4;
      case "netns": return 1.5;
      case "exposed": return 0.8;
      default: return 0;
    }
  }

  function linkParticles(l: GLink): number {
    if (app.mode !== "reachability" || !linkLit(l)) return 0;
    if (l.kind === "reach") return l.dns ? 2 : 0;
    return l.kind === "exposed" ? 2 : 0;
  }

  function linkLabel(l: GLink): string {
    if (l.kind === "reach") return `reachable via ${(l.networks ?? []).join(", ")}${l.dns ? " (DNS)" : " (IP only, no DNS)"}`;
    if (l.kind === "exposed") return `published: ${(l.ports ?? []).join(", ")}`;
    if (l.kind === "netns") return "shares network namespace";
    return l.kind;
  }

  function focusSelected(): void {
    const n = app.selectedId ? app.graph?.nodes.get(app.selectedId) : undefined;
    if (!fg || !n || n.x === undefined || n.y === undefined || n.z === undefined) return;
    const dist = Math.hypot(n.x, n.y, n.z);
    const ratio = dist > 1 ? 1 + 90 / dist : 1;
    const pos = dist > 1 ? { x: n.x * ratio, y: n.y * ratio, z: n.z * ratio } : { x: 0, y: 0, z: 90 };
    fg.cameraPosition(pos, { x: n.x, y: n.y, z: n.z }, 900);
  }

  onMount(() => {
    const graph = new ForceGraph3D(el, { controlType: "orbit" }) as unknown as ForceGraph3DInstance<GNode, GLink>;
    fg = graph
      .backgroundColor("#0d1117")
      .showNavInfo(false)
      .nodeId("id")
      .nodeLabel((n) => `${KIND_LABEL_SHORT(n.kind)} ${displayName(n)}${n.status ? ` · ${n.status}` : ""}`)
      .nodeThreeObject(nodeObject)
      .linkColor(linkColor)
      .linkWidth(linkWidth)
      .linkOpacity(0.7)
      .linkLabel(linkLabel)
      .linkDirectionalParticles(linkParticles)
      .linkDirectionalParticleSpeed(0.006)
      .linkDirectionalParticleWidth(1.6)
      .onNodeClick((n) => app.select(n.id, true))
      .onBackgroundClick(() => app.select(null))
      .onNodeHover((n) => {
        hovered = n?.id ?? null;
        el.style.cursor = n ? "pointer" : "";
        repaint();
      });
    resize = new ResizeObserver(() => fg?.width(el.clientWidth).height(el.clientHeight));
    resize.observe(el);
  });

  function KIND_LABEL_SHORT(kind: Kind): string {
    return kind === "hostnet" ? "host network" : kind;
  }

  onDestroy(() => {
    resize?.disconnect();
    fg?._destructor();
  });

  // Data: re-run whenever the store or a filter changes.
  $effect(() => {
    void app.graph?.version;
    void app.mode;
    void app.hiddenKinds;
    void app.hideStopped;
    void app.active?.displayName;
    if (!fg) return;
    const data = visibleData();
    visibleNodes = data.nodes.length;
    highlight = computeHighlight();
    fg.graphData(data);
    repaint();
  });

  // Highlight: re-run on selection changes.
  $effect(() => {
    void app.selectedId;
    void app.graph?.version;
    highlight = computeHighlight();
    repaint();
  });

  // Camera: re-run on explicit focus requests.
  $effect(() => {
    if (app.focusRequest > 0) focusSelected();
  });

  // Switching hosts frames the new scene.
  $effect(() => {
    void app.activeHost;
    setTimeout(() => fg?.zoomToFit(600, 40), 1200);
  });
</script>

<div class="graph" bind:this={el} data-testid="graph" data-visible-nodes={visibleNodes}></div>

<style>
  .graph {
    position: absolute;
    inset: 0;
    overflow: hidden;
  }
</style>
