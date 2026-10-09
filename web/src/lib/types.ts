// Mirrors of the Go DTOs. Keep in sync with internal/model, internal/hub, internal/docker
// and internal/server.

export type Kind =
  | "host"
  | "container"
  | "network"
  | "hostnet"
  | "volume"
  | "bind"
  | "image"
  | "project"
  | "service"
  | "task"
  | "node"
  | "secret"
  | "config";

export type LinkKind =
  | "host"
  | "network"
  | "mount"
  | "image"
  | "project"
  | "task"
  | "runs"
  | "placed"
  | "secret"
  | "config"
  | "reach"
  | "netns"
  | "hostnet"
  | "exposed";

export interface Node {
  id: string;
  kind: Kind;
  name: string;
  status?: string;
  group?: string;
  attrs?: Record<string, string>;
}

export interface Link {
  id: string;
  source: string;
  target: string;
  kind: LinkKind;
  networks?: string[];
  dns?: boolean;
  ports?: string[];
}

export interface Graph {
  nodes: Node[];
  links: Link[];
}

export interface Diff {
  addNodes?: Node[];
  updateNodes?: Node[];
  removeNodes?: string[];
  addLinks?: Link[];
  updateLinks?: Link[];
  removeLinks?: string[];
}

export type HostStatus = "connecting" | "connected" | "error";

export interface HostInfo {
  name: string;
  displayName: string;
  url: string;
  status: HostStatus;
  error?: string;
  engineVersion?: string;
}

export interface Related extends Node {
  link: LinkKind;
  direction: "in" | "out";
}

export interface Entity<T = unknown> {
  node: Node;
  related: Related[];
  data?: T;
}

export interface PeerNetwork {
  network: string;
  dns: boolean;
  names?: string[];
  ip?: string;
}

export interface Peer {
  id: string;
  name: string;
  state: string;
  networks: PeerNetwork[];
}

export interface Ref {
  id: string;
  name: string;
}

export interface ContainerDetails {
  id: string;
  name: string;
  image: string;
  imageId: string;
  command: string[];
  created: string;
  state: {
    status: string;
    running: boolean;
    exitCode: number;
    error?: string;
    oomKilled: boolean;
    startedAt: string;
    finishedAt: string;
    health?: string;
    failingStreak?: number;
  };
  restartPolicy: string;
  restartCount: number;
  ports: { container: string; hostIp?: string; hostPort?: string }[];
  mounts: { type: string; name?: string; source?: string; destination: string; rw: boolean }[];
  networks: {
    network: string;
    nodeId: string;
    ip?: string;
    ipv6?: string;
    gateway?: string;
    mac?: string;
    aliases?: string[];
    dnsNames?: string[];
  }[];
  networkMode: string;
  netnsOwner?: Ref;
  netnsSharers?: Ref[];
  peers: Peer[];
  labels: Record<string, string>;
  resources: { memory: number; nanoCpus: number; pidsLimit?: number };
  project?: string;
  service?: string;
  hostname: string;
  user?: string;
  workingDir?: string;
  tty: boolean;
}

export interface NetworkDetails {
  id: string;
  name: string;
  driver: string;
  scope: string;
  internal: boolean;
  attachable: boolean;
  ingress: boolean;
  icc: boolean;
  dns: boolean;
  ipv4: boolean;
  ipv6: boolean;
  subnets: { subnet: string; gateway?: string }[];
  options: Record<string, string> | null;
  labels: Record<string, string> | null;
  members: { id: string; name: string; ipv4?: string; ipv6?: string }[];
  reachGroup: boolean;
}

export interface StatsSample {
  ts: number;
  cpuPct: number;
  memUsage: number;
  memLimit: number;
  netRx: number;
  netTx: number;
  blkRead: number;
  blkWrite: number;
  pids: number;
}

export interface LogLine {
  stream: "stdout" | "stderr";
  ts?: string;
  text: string;
}
