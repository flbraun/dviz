import { execFileSync, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const repo = resolve(import.meta.dirname, "..", "..");
const dindImage = "docker:29-dind";
const image = "busybox:1.37";

function docker(...args: string[]): string {
  return execFileSync("docker", args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/** Starts dviz with a local + dind config and creates fixtures; returns the teardown. */
export default async function globalSetup(): Promise<() => Promise<void>> {
  const run = randomBytes(3).toString("hex");
  const label = `dviz.e2e.run=${run}`;
  const cleanup = () => {
    const ids = docker("ps", "-aq", "--filter", `label=${label}`).split("\n").filter(Boolean);
    if (ids.length) docker("rm", "-fv", ...ids);
    const nets = docker("network", "ls", "-q", "--filter", `label=${label}`).split("\n").filter(Boolean);
    if (nets.length) docker("network", "rm", ...nets);
  };
  try {
    return await setup(run, label, cleanup);
  } catch (e) {
    cleanup();
    throw e;
  }
}

async function setup(run: string, label: string, cleanup: () => void): Promise<() => Promise<void>> {
  const p = (s: string) => `e2e-${run}-${s}`;
  const secret = `e2e-secret-${run}-value`;
  const dir = mkdtempSync(join(tmpdir(), "dviz-e2e-"));

  const bin = process.env.DVIZ_BIN ?? join(dir, "dviz");
  if (!process.env.DVIZ_BIN) {
    execFileSync("npm", ["run", "build"], { cwd: join(repo, "web"), stdio: "inherit" });
    execFileSync("go", ["build", "-o", bin, "./cmd/dviz"], { cwd: repo, env: { ...process.env, CGO_ENABLED: "0" }, stdio: "inherit" });
  }

  for (const img of [image, dindImage]) {
    try {
      docker("image", "inspect", img);
    } catch {
      docker("pull", img);
    }
  }

  // Second host: docker:dind over tcp+TLS.
  const dind = docker("run", "-d", "--privileged", "--label", label, "-e", "DOCKER_TLS_CERTDIR=/certs", "-p", "127.0.0.1::2376", dindImage);
  const dindPort = docker("port", dind, "2376/tcp").split("\n")[0]!.split(":").pop()!;
  const certs = join(dir, "certs");
  const dindArgs = () => ["--host", `tcp://127.0.0.1:${dindPort}`, "--tlsverify", "--tlscacert", join(certs, "ca.pem"), "--tlscert", join(certs, "cert.pem"), "--tlskey", join(certs, "key.pem")];
  for (let i = 0; ; i++) {
    try {
      mkdirSync(certs, { recursive: true });
      docker("cp", `${dind}:/certs/client/.`, certs);
      docker(...dindArgs(), "version");
      break;
    } catch (e) {
      if (i > 90) throw e;
      await sleep(1000);
    }
  }
  docker(...dindArgs(), "run", "-d", "--name", p("dind-only"), image, "sleep", "3600");

  // Local fixtures.
  docker("network", "create", "--label", label, p("n1"));
  docker("network", "create", "--label", label, p("n2"));
  const ctr = (...args: string[]) => docker("run", "-d", "--label", label, ...args);
  ctr("--name", p("a"), "--network", p("n1"), image, "sleep", "3600");
  ctr("--name", p("b"), "--network", p("n1"), "--network-alias", "b-alias", image, "sleep", "3600");
  ctr("--name", p("c"), "--network", p("n2"), image, "sleep", "3600");
  ctr("--name", p("app"), "-e", `SECRET_TOKEN=${secret}`, "-p", "127.0.0.1::8080", image, "sh", "-c", "echo hello-e2e; while true; do echo tick; sleep 1; done");
  ctr("--name", p("stopme"), image, "sleep", "3600");

  writeFileSync(
    join(dir, "dviz.yml"),
    `listen: 127.0.0.1:0
hosts:
  - name: local
    display_name: "Local daemon"
    url: ${process.env.DOCKER_HOST ?? "unix:///var/run/docker.sock"}
  - name: dind
    display_name: "Docker in Docker"
    url: tcp://127.0.0.1:${dindPort}
    tls:
      ca: certs/ca.pem
      cert: certs/cert.pem
      key: certs/key.pem
`,
  );

  const proc = spawn(bin, [], { cwd: dir, stdio: ["ignore", "pipe", "pipe"] });
  const baseURL = await new Promise<string>((resolveURL, reject) => {
    let log = "";
    const onData = (b: Buffer) => {
      log += b.toString();
      const m = /listening url=(http:\/\/\S+)/.exec(log);
      if (m) resolveURL(m[1]!);
    };
    proc.stdout.on("data", onData);
    proc.stderr.on("data", onData);
    proc.on("exit", (code) => reject(new Error(`dviz exited (${code}):\n${log}`)));
    setTimeout(() => reject(new Error(`dviz did not start:\n${log}`)), 15_000);
  });

  process.env.DVIZ_URL = baseURL;
  process.env.E2E_PREFIX = `e2e-${run}-`;
  process.env.E2E_SECRET = secret;

  return async () => {
    proc.kill("SIGTERM");
    cleanup();
  };
}
