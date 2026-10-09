import type { Diff, Graph, Link, Node } from "./types";

/** A node as held by the store; 3d-force-graph adds layout fields (x, y, z, vx, ...). */
export interface GNode extends Node {
  x?: number;
  y?: number;
  z?: number;
}

/**
 * A link as held by the store. 3d-force-graph replaces source/target with node objects,
 * so the original endpoint IDs are kept in sourceId/targetId.
 */
export interface GLink extends Omit<Link, "source" | "target"> {
  source: string | GNode;
  target: string | GNode;
  sourceId: string;
  targetId: string;
}

/**
 * Holds one host's graph. Updates are applied in place: an existing node keeps its object
 * identity (and therefore its layout position) across snapshots and diffs.
 */
export class GraphStore {
  readonly nodes = new Map<string, GNode>();
  readonly links = new Map<string, GLink>();
  /** Incremented on every change; read it to subscribe to updates. */
  version = $state(0);
  /** Whether a snapshot has been received. */
  loaded = $state(false);

  applySnapshot(g: Graph): void {
    const nodeIds = new Set(g.nodes.map((n) => n.id));
    const linkIds = new Set(g.links.map((l) => l.id));
    for (const id of this.nodes.keys()) if (!nodeIds.has(id)) this.nodes.delete(id);
    for (const id of this.links.keys()) if (!linkIds.has(id)) this.links.delete(id);
    for (const n of g.nodes) this.upsertNode(n);
    for (const l of g.links) this.upsertLink(l);
    this.dropDangling();
    this.loaded = true;
    this.version++;
  }

  applyDiff(d: Diff): void {
    for (const id of d.removeNodes ?? []) this.nodes.delete(id);
    for (const id of d.removeLinks ?? []) this.links.delete(id);
    for (const n of [...(d.addNodes ?? []), ...(d.updateNodes ?? [])]) this.upsertNode(n);
    for (const l of [...(d.addLinks ?? []), ...(d.updateLinks ?? [])]) this.upsertLink(l);
    this.dropDangling();
    this.version++;
  }

  private upsertNode(n: Node): void {
    const cur = this.nodes.get(n.id);
    if (!cur) {
      this.nodes.set(n.id, { ...n });
      return;
    }
    // Replace data fields but keep the object (and its layout fields).
    cur.kind = n.kind;
    cur.name = n.name;
    cur.status = n.status;
    cur.group = n.group;
    cur.attrs = n.attrs;
  }

  private upsertLink(l: Link): void {
    const cur = this.links.get(l.id);
    const next: GLink = { ...l, sourceId: l.source, targetId: l.target };
    if (!cur) {
      this.links.set(l.id, next);
      return;
    }
    Object.assign(cur, next);
  }

  /** Removes links whose endpoints no longer exist; the force layout cannot resolve them. */
  private dropDangling(): void {
    for (const [id, l] of this.links) {
      if (!this.nodes.has(l.sourceId) || !this.nodes.has(l.targetId)) this.links.delete(id);
    }
  }

  /** Nodes linked to id by links of the given kinds (all kinds if omitted). */
  neighbors(id: string, kinds?: ReadonlySet<string>): Set<string> {
    const out = new Set<string>();
    for (const l of this.links.values()) {
      if (kinds && !kinds.has(l.kind)) continue;
      if (l.sourceId === id) out.add(l.targetId);
      else if (l.targetId === id) out.add(l.sourceId);
    }
    return out;
  }
}
