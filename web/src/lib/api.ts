import type { Entity, HostInfo } from "./types";

export function hostPath(host: string): string {
  return `/api/hosts/${encodeURIComponent(host)}`;
}

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, { signal });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = ((await res.json()) as { error?: string }).error ?? msg;
    } catch {
      // not JSON
    }
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

export function fetchHosts(): Promise<HostInfo[]> {
  return getJSON("/api/hosts");
}

export function fetchEntity<T>(host: string, id: string, signal?: AbortSignal): Promise<Entity<T>> {
  return getJSON(`${hostPath(host)}/entities/${encodeURIComponent(id)}`, signal);
}

export type Handlers = Record<string, (data: unknown) => void>;

/**
 * Opens an EventSource and dispatches named events with parsed JSON payloads.
 * EventSource reconnects on its own; streams that end deliberately send "eof".
 */
export function subscribe(url: string, handlers: Handlers): () => void {
  const es = new EventSource(url);
  for (const [event, fn] of Object.entries(handlers)) {
    es.addEventListener(event, (e) => {
      const data: unknown = JSON.parse((e as MessageEvent<string>).data);
      fn(data);
      if (event === "eof") es.close();
    });
  }
  return () => es.close();
}
