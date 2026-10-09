# dviz

dviz connects to one or more Docker daemons and shows what runs on them as an interactive 3D graph in your browser: containers, networks, volumes, bind mounts, images, compose projects, and swarm services, tasks, nodes, secrets and configs. Each daemon gets its own scene. The graph updates live from the Docker event stream. A **Reachability** view shows which containers and services can talk to each other over the network. Selecting an entity opens its details, including live stats and logs for containers.

dviz is read-only and ships as a single static binary with the web UI embedded.

## Quick start

dviz takes no command-line arguments and has no built-in configuration: it needs a [`dviz.yml`](#configuration) and refuses to start without one. A minimal file for the local daemon:

```yaml
listen: 127.0.0.1:8080
hosts:
  - name: local
    url: unix:///var/run/docker.sock
```

```sh
./dviz                        # with dviz.yml in the current directory; serves http://127.0.0.1:8080
```

With Docker, mount the config at `/dviz.yml` and set `listen: 0.0.0.0:8080` in it so the published port reaches dviz:

```sh
docker build -t dviz .
docker run --rm -p 127.0.0.1:8080:8080 \
  -v "$PWD/dviz.yml:/dviz.yml:ro" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  --group-add "$(stat -c %g /var/run/docker.sock)" \
  dviz
```

The image runs as an unprivileged user (`--group-add` grants socket access). The image has no home directory, so for [SSH hosts](#ssh-hosts) mount the `known_hosts` file (and key or password file) and set their paths explicitly.

## Development

Prerequisites:
- Go 1.27+
- Node 26+ with npm
- Access to a Docker daemon: your user is in the `docker` group, or set `DOCKER_HOST`.
- The integration and e2e tests also start a privileged `docker:dind` container.

```sh
npm --prefix web ci          # install frontend dependencies
go generate ./web            # build the frontend into web/dist (embedded by go build)
$EDITOR dviz.yml             # required, see Quick start; gitignored in the repo root
go run ./cmd/dviz            # API + UI on the configured listen address
npm --prefix web run dev     # optional: Vite dev server with hot reload on :5173, proxies /api to :8080
```

Checks and tests:

```sh
npm --prefix web run check                  # svelte-check / TypeScript
npm --prefix web test                       # vitest (frontend logic)
go vet ./... && go test ./...               # Go; integration tests need Docker, skip without it
npx --prefix web playwright install chromium
npm --prefix web run e2e                    # Playwright against the real binary, local daemon and dind
```

Production build (always static, never CGO):

```sh
npm --prefix web ci && go generate ./web
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" ./cmd/dviz
```

## Configuration

dviz reads a single YAML file, `dviz.yml`. It uses the first one it finds in this order:

1. `$(pwd)/dviz.yml`
2. `~/.config/dviz/dviz.yml`
3. `/etc/dviz/dviz.yml`

A config file is required. If none of these locations has one, dviz exits with an error listing the paths it searched. The defaults in the table below only fill in keys that are missing from an existing file.

**Live reload.** dviz watches all three locations and applies changes without a restart:
- Added hosts connect, and removed hosts disconnect and disappear from the UI.
- Changing a host's `url` or `tls` reconnects it. Changing `display_name` updates the label in place.
- Changing `listen` moves the server to the new address. Open browser tabs need to be pointed at it.
- Creating a file in a higher-priority location makes it take over. Deleting one falls back to the next location. Deleting the last one keeps the running config until a valid file appears again.
- An invalid file is logged and ignored, and the last valid config stays active.

The file is also validated at startup, and an invalid file prevents startup.

### Reference

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `listen` | string | `127.0.0.1:8080` | Address to serve on, as `host:port`. The host must be an IP address or `localhost` (empty means all interfaces). Port `0` picks a free port. When `host` is a loopback address, requests whose `Host` header isn't a loopback name are rejected (DNS-rebinding protection). |
| `hosts` | list | one host: `local` → `unix:///var/run/docker.sock` | Docker daemons to show, one scene each, in this order. An empty or missing list uses the default. |
| `hosts[].name` | string | — (required) | Stable identifier used in URLs (`#/<name>`). Must match `^[a-z0-9][a-z0-9_-]*$` and be unique. |
| `hosts[].display_name` | string | `name` | Human-readable label shown in the UI. |
| `hosts[].url` | string | — (required) | `unix:///path/to/docker.sock`, `tcp://host:port`, or `ssh://[user@]host[:port][/path/to/docker.sock]` (see [SSH hosts](#ssh-hosts)). For SSH, the user defaults to the current user, the port to `22` and the remote socket to `/var/run/docker.sock`. Passwords in URLs are rejected. |
| `hosts[].tls` | object | none | Client TLS for `tcp://` hosts. Not allowed with `unix://` or `ssh://`. |
| `hosts[].tls.ca` | path | system roots | CA bundle used to verify the daemon. |
| `hosts[].tls.cert` | path | none | Client certificate. Requires `key`. |
| `hosts[].tls.key` | path | none | Client private key. Requires `cert`. |
| `hosts[].tls.insecure_skip_verify` | bool | `false` | Skip server certificate verification. |
| `hosts[].ssh` | object | — (required for `ssh://`) | Settings for `ssh://` hosts. Only allowed with `ssh://`. |
| `hosts[].ssh.method` | string | — (required) | How to authenticate: `password`, `agent` or `key`. Only this method is used. |
| `hosts[].ssh.password` | path | none | File containing the login password (required for `password`), or the passphrase of a protected `identity_file` (optional for `key`). One trailing newline is ignored. Not allowed with `agent`. |
| `hosts[].ssh.identity_file` | path | none | Private key for method `key` (required there, not allowed otherwise). |
| `hosts[].ssh.known_hosts` | path | `~/.ssh/known_hosts` | File used to verify the server's host key. |
| `hosts[].ssh.insecure_ignore_host_key` | bool | `false` | Skip host key verification. |

Unknown keys are rejected. TLS and SSH paths may be absolute, start with `~/`, or be relative to the directory containing `dviz.yml`.

### Examples

Local daemon with a friendly name:

```yaml
hosts:
  - name: local
    display_name: Workstation
    url: unix:///var/run/docker.sock
```

Several daemons, one over TCP with mutual TLS:

```yaml
listen: 127.0.0.1:9000
hosts:
  - name: local
    display_name: Workstation
    url: unix:///var/run/docker.sock
  - name: prod
    display_name: Prod (eu-1)
    url: tcp://10.0.0.5:2376
    tls:
      ca: certs/prod/ca.pem
      cert: certs/prod/cert.pem
      key: certs/prod/key.pem
```

Remote servers over SSH, verified against `~/.ssh/known_hosts`:

```yaml
hosts:
  - name: prod
    display_name: Prod server
    url: ssh://deploy@prod.example.com
    ssh:
      method: agent                    # keys from the running ssh-agent
  - name: staging
    url: ssh://deploy@staging.example.com:2222
    ssh:
      method: key
      identity_file: ~/.ssh/dviz_ed25519
      password: ~/.config/dviz/staging.passphrase   # only if the key is protected
  - name: lab
    url: ssh://admin@lab.local/run/user/1000/docker.sock   # rootless Docker socket
    ssh:
      method: password
      password: ~/.config/dviz/lab.password
```

Inside the Docker image (mounted at `/dviz.yml`):

```yaml
listen: 0.0.0.0:8080
hosts:
  - name: local
    url: unix:///var/run/docker.sock
```

### SSH hosts

dviz connects to `ssh://` hosts itself, so it doesn't need the `ssh` binary, and the server doesn't need the docker CLI. It opens the remote Docker socket through SSH socket forwarding, the same mechanism as `ssh -L local.sock:/var/run/docker.sock`. It keeps one SSH connection per host, sends keepalives every 30 seconds, and reconnects automatically when the connection drops.

**Authentication.** Exactly one method is used, set by `ssh.method`. dviz never searches for keys or credentials on its own.
- `password`: the password is read from the file at `ssh.password`. Keep that file readable only by you (`chmod 600`). The server must allow `PasswordAuthentication`; keyboard-interactive login is not supported.
- `agent`: dviz uses the keys of the ssh-agent at `$SSH_AUTH_SOCK`, from dviz's environment at the time it connects.
- `key`: dviz uses the private key at `ssh.identity_file`. For a passphrase-protected key, `ssh.password` names a file holding the passphrase.

**Host key verification.** The server's key must already be listed in `known_hosts`. Connect once with `ssh` to add it.
- An unknown key or a mismatched key puts the host in an error state, and the error shows the key's fingerprint.
- `~/.ssh/config` is not read, so aliases, `ProxyJump` and per-host options there don't apply.

**Server requirements:**
- The SSH user can read and write the remote Docker socket, for example by being in the `docker` group.
- sshd allows `AllowTcpForwarding` (at least `local`) and `AllowStreamLocalForwarding`. Both default to yes in OpenSSH, but some distributions (for example Alpine) disable TCP forwarding, which also blocks socket forwarding.

## Reachability

In Reachability mode, a line between two containers (or swarm services) means they can reach each other:

- **Shared network.** Containers on a user-defined network reach each other by name through Docker's embedded DNS. These lines are thick and colored per network.
- **Default `bridge`.** Containers there reach each other by IP only, with no DNS. These lines are thin.
- **Exceptions:**
  - Networks with `com.docker.network.bridge.enable_icc=false` and the swarm ingress network create no peer links.
  - `network_mode: container:<x>` shares x's namespace, and with it x's peers (orange link).
  - `network_mode: host` links to the host network.
  - `network_mode: none` is shown as isolated (grey ring).
- **Published ports** link a container or service to the host.
- **Large networks.** Networks with more than 50 members are drawn as a hub, not as pairwise links.

The container details' **Network** tab lists every reachable peer with the DNS names and IPs it can be reached under.

## Security notes

- **Docker socket access is root-equivalent.** Anyone who can reach the dviz UI can see everything dviz can see. dviz listens on loopback by default and has no authentication. Put a reverse proxy with authentication in front of it if you expose it.
- **The API is read-only.** It has no endpoint that changes daemon state.
- **What is stripped.** Secrets are removed at the read boundary, before any other code sees the data:
  - environment variable values, for containers, images and services (keys stay visible)
  - swarm secret and config payloads
  - volume, mount, network-attachment and log driver option values
  - healthcheck output
  - attributes of Docker events
- **What is not stripped.** Image history is never fetched. Command lines, labels and container logs are shown as Docker reports them, so don't put secrets there.

## License

dviz is free software, licensed under the [GNU General Public License v3.0](https://github.com/flbraun/dviz/blob/master/LICENSE).

## AI disclosure

This app's code is entirely written by AI. All changes are reviewed, tested and fully understood by human maintainers before they end up in a public release.
