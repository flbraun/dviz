import { hostPath, subscribe } from "./api";
import { GraphStore } from "./graphStore.svelte";
import type { Diff, Graph, HostInfo, Kind } from "./types";

export type ViewMode = "topology" | "reachability";

export const ALL_KINDS: Kind[] = [
  "host",
  "project",
  "container",
  "network",
  "hostnet",
  "volume",
  "bind",
  "image",
  "service",
  "task",
  "node",
  "secret",
  "config",
];

/** Kinds shown in reachability mode; the rest are structural only. */
export const REACH_KINDS: ReadonlySet<Kind> = new Set<Kind>(["host", "project", "container", "service", "hostnet"]);

class AppState {
  hosts = $state<HostInfo[]>([]);
  hostsLoaded = $state(false);
  activeHost = $state<string | null>(null);
  selectedId = $state<string | null>(null);
  /** Incremented to ask the 3D view to move the camera to the selection. */
  focusRequest = $state(0);
  mode = $state<ViewMode>("topology");
  hiddenKinds = $state<Kind[]>(["image", "secret", "config"]);
  hideStopped = $state(false);
  query = $state("");

  private stores = new Map<string, GraphStore>();
  private unsubGraph: (() => void) | null = null;

  get active(): HostInfo | undefined {
    return this.hosts.find((h) => h.name === this.activeHost);
  }

  /** Returns the store of a host, creating it on first use. Stores persist to keep layouts. */
  store(host: string): GraphStore {
    let s = this.stores.get(host);
    if (!s) {
      s = new GraphStore();
      this.stores.set(host, s);
    }
    return s;
  }

  get graph(): GraphStore | null {
    return this.activeHost ? this.store(this.activeHost) : null;
  }

  start(): void {
    subscribe("/api/events", {
      hosts: (data) => {
        this.hosts = data as HostInfo[];
        this.hostsLoaded = true;
        const names = new Set(this.hosts.map((h) => h.name));
        for (const name of this.stores.keys()) if (!names.has(name)) this.stores.delete(name);
        this.syncFromHash();
      },
    });
    window.addEventListener("hashchange", () => this.syncFromHash());
  }

  private syncFromHash(): void {
    const fromHash = decodeURIComponent(location.hash.replace(/^#\/?/, ""));
    const names = this.hosts.map((h) => h.name);
    const next = names.includes(fromHash) ? fromHash : (names[0] ?? null);
    if (next !== this.activeHost) this.switchHost(next);
    else if (next && fromHash !== next) history.replaceState(null, "", `#/${encodeURIComponent(next)}`);
  }

  switchHost(name: string | null): void {
    this.unsubGraph?.();
    this.unsubGraph = null;
    this.activeHost = name;
    this.selectedId = null;
    if (!name) return;
    if (location.hash !== `#/${encodeURIComponent(name)}`) history.replaceState(null, "", `#/${encodeURIComponent(name)}`);
    const store = this.store(name);
    this.unsubGraph = subscribe(`${hostPath(name)}/graph/events`, {
      snapshot: (g) => store.applySnapshot(g as Graph),
      diff: (d) => store.applyDiff(d as Diff),
    });
  }

  select(id: string | null, focus = false): void {
    this.selectedId = id;
    if (id && focus) this.focusRequest++;
  }

  toggleKind(kind: Kind): void {
    this.hiddenKinds = this.hiddenKinds.includes(kind)
      ? this.hiddenKinds.filter((k) => k !== kind)
      : [...this.hiddenKinds, kind];
  }
}

export const app = new AppState();
