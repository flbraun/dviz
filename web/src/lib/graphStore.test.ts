import { describe, expect, it } from "vitest";
import { GraphStore } from "./graphStore.svelte";
import type { Graph } from "./types";

const graph = (): Graph => ({
  nodes: [
    { id: "host", kind: "host", name: "h" },
    { id: "container/a", kind: "container", name: "a", status: "running" },
    { id: "container/b", kind: "container", name: "b", status: "running" },
  ],
  links: [
    { id: "host|host|container/a", source: "host", target: "container/a", kind: "host" },
    { id: "container/a|reach|container/b", source: "container/a", target: "container/b", kind: "reach", networks: ["n1"], dns: true },
  ],
});

describe("GraphStore", () => {
  it("keeps node identity and layout across snapshots and diffs", () => {
    const s = new GraphStore();
    s.applySnapshot(graph());
    const a = s.nodes.get("container/a")!;
    Object.assign(a, { x: 1, y: 2, z: 3 });

    s.applyDiff({ updateNodes: [{ id: "container/a", kind: "container", name: "a", status: "exited" }] });
    expect(s.nodes.get("container/a")).toBe(a);
    expect(a.status).toBe("exited");
    expect([a.x, a.y, a.z]).toEqual([1, 2, 3]);

    s.applySnapshot(graph());
    expect(s.nodes.get("container/a")).toBe(a);
    expect(a.status).toBe("running");
  });

  it("drops links whose endpoints were removed", () => {
    const s = new GraphStore();
    s.applySnapshot(graph());
    // The server removes the node; a diff that omits the link removal must not leave a
    // dangling link the force layout would choke on.
    s.applyDiff({ removeNodes: ["container/b"] });
    expect(s.links.has("container/a|reach|container/b")).toBe(false);
    expect(s.links.size).toBe(1);
  });

  it("restores endpoint IDs after the layout replaced them with objects", () => {
    const s = new GraphStore();
    s.applySnapshot(graph());
    const l = s.links.get("container/a|reach|container/b")!;
    l.source = s.nodes.get("container/a")!;
    s.applyDiff({ updateLinks: [{ ...graph().links[1]!, networks: ["n1", "n2"] }] });
    expect(l.sourceId).toBe("container/a");
    expect(l.networks).toEqual(["n1", "n2"]);
    expect(s.neighbors("container/a", new Set(["reach"]))).toEqual(new Set(["container/b"]));
  });
});
