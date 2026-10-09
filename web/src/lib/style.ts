import type { Kind, LinkKind, Node } from "./types";

export const KIND_LABEL: Record<Kind, string> = {
  host: "Host",
  project: "Project / stack",
  container: "Container",
  network: "Network",
  hostnet: "Host network",
  volume: "Volume",
  bind: "Bind mount",
  image: "Image",
  service: "Service",
  task: "Task",
  node: "Swarm node",
  secret: "Secret",
  config: "Config",
};

export const KIND_COLOR: Record<Kind, string> = {
  host: "#e6edf3",
  project: "#d2a8ff",
  container: "#3fb950",
  network: "#58a6ff",
  hostnet: "#f0883e",
  volume: "#e3b341",
  bind: "#bb8009",
  image: "#8b949e",
  service: "#56d4dd",
  task: "#7ee2b8",
  node: "#ff7b72",
  secret: "#f778ba",
  config: "#a5d6ff",
};

const STATE_COLOR: Record<string, string> = {
  running: "#3fb950",
  healthy: "#3fb950",
  starting: "#d29922",
  restarting: "#d29922",
  unhealthy: "#f85149",
  dead: "#f85149",
  paused: "#58a6ff",
  created: "#6e7681",
  exited: "#6e7681",
  removing: "#6e7681",
};

export function nodeColor(n: Node): string {
  if (n.kind === "container") {
    const health = n.attrs?.["health"];
    if (n.status === "running" && health) return STATE_COLOR[health] ?? KIND_COLOR.container;
    return STATE_COLOR[n.status ?? ""] ?? "#6e7681";
  }
  if (n.kind === "host" && n.status === "error") return "#f85149";
  if (n.kind === "task" && n.status !== "running") return "#6e7681";
  return KIND_COLOR[n.kind];
}

export function isStopped(n: Node): boolean {
  return (n.kind === "container" && n.status !== "running") || (n.kind === "task" && n.status !== "running");
}

export const LINK_COLOR: Partial<Record<LinkKind, string>> = {
  netns: "#f0883e",
  hostnet: "#f0883e",
  exposed: "#ffa657",
  mount: "#e3b341",
  image: "#6e7681",
  secret: "#f778ba",
  config: "#a5d6ff",
};

/** Stable color per network name for reach links. */
export function networkColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return `hsl(${h % 360}, 80%, 65%)`;
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1);
  return `${(n / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}
