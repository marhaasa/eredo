# Claude Code sandboxes: how the two setups differ

Both commands run Claude Code against a repo you keep working on from the
host with your own editor, git and credentials. They answer different
questions:

- `moat` answers *what is the agent allowed to do?* It is policy inside
  a container you control completely.
- `moat-docker` answers *what if the agent fully compromises its
  environment?* It is a microVM boundary with the credential kept outside.

Everything below was verified on 2026-09-24 with Docker Desktop 29.x and the
`docker sandbox` plugin v0.12.

## 1. Topology

### moat: container plus proxy sidecar

```mermaid
flowchart TB
  subgraph host["host"]
    direction LR
    you["you: editor, git push,<br/>credentials"]
    repo[("~/src/project")]
    kc["Keychain:<br/>Claude OAuth token"]
    script["moat"]
    you --> repo
  end
  subgraph vm["Docker VM (Desktop) or host kernel (Linux Engine)"]
    direction LR
    subgraph inet["moat-project-net: internal, no gateway"]
      sbx["moat-project<br/>node user, cap-drop ALL, no sudo<br/>HTTPS_PROXY=proxy:3128"]
      px["moat-project-proxy<br/>squid: CONNECT :443<br/>to allowlist only"]
    end
    egress["moat-egress network"]
    sbx -- "CONNECT api.anthropic.com" --> px --> egress
  end
  net["internet: allowlisted domains"]
  repo -. "bind mount rw<br/>.git/hooks = empty ro tmpfs<br/>.git/config = ro bind" .-> sbx
  kc -- "docker exec -e token,<br/>per attach" --> sbx
  script -- "creates, bootstraps" --> sbx
  script -- "allowlist" --> px
  egress --> net
```

The sandbox has no route to the internet at all. The only thing it can reach
is the proxy, and the proxy owns the allowlist. Nothing running inside can
widen it, because there is no sudo, no capability and no iptables to touch.

### moat-docker: microVM plus Docker's host proxy

```mermaid
flowchart TB
  subgraph host["host"]
    direction LR
    you["you: editor, git push,<br/>credentials"]
    repo[("~/src/project")]
    cred["Claude session token<br/>stored by Docker after /login"]
    dproxy["Docker host proxy<br/>default deny + allowlist<br/>TLS interception,<br/>credential injection"]
    script["moat-docker"]
    you --> repo
    cred --> dproxy
  end
  subgraph mvm["microVM moat-project: own kernel"]
    agent["claude<br/>agent user, sudo removed by the script<br/>HTTPS_PROXY=host.docker.internal:3128"]
  end
  net["internet: allowlisted domains"]
  repo -. "shared at the same path<br/>.git fully writable" .-> agent
  script -- "policy, settings, hook,<br/>identity, drop sudo" --> agent
  script -- "network policy" --> dproxy
  agent -- "HTTPS via proxy" --> dproxy
  dproxy --> net
```

Docker ships this with an allow-all policy, passwordless root, a Docker
daemon inside and Claude in bypassPermissions mode. The script replaces all of
that with the same allowlist, settings and privileges moat has, so the
remaining differences are the ones Docker's design forces.

## 2. Where a compromise ends up

```mermaid
flowchart TB
  subgraph A["moat"]
    direction TB
    a1["Claude process"] -->|"container escape"| a2["Docker VM<br/>every other container<br/>every shared host path"]
    a2 -->|"VM escape"| a3["host"]
  end
  subgraph B["moat-docker"]
    direction TB
    b1["Claude process"] -->|"container escape"| b2["microVM<br/>this project only"]
    b2 -->|"hypervisor escape"| b3["host"]
  end
```

Both need two escapes to reach the host. The difference is what the first
one lands in. In moat that is a kernel shared with everything else you run in
Docker, plus whatever Docker Desktop's file sharing exposes, which is your
whole home directory by default on macOS unless you narrow it. On Linux with
Docker Engine there is no VM in between at all, so the first escape is the
last. In the microVM the first landing is a kernel that holds nothing but this
project.

## 3. What an outbound request goes through

```mermaid
sequenceDiagram
  participant C as claude in moat
  participant S as squid sidecar
  participant I as internet
  C->>S: CONNECT api.anthropic.com:443
  S->>S: dstdomain allowlist check
  S-->>C: 200 tunnel established
  C->>I: TLS end to end, squid sees only the hostname
  C->>S: CONNECT example.com:443
  S-->>C: 403, logged as TCP_DENIED
```

```mermaid
sequenceDiagram
  participant C as claude in moat-docker
  participant P as Docker host proxy
  participant I as internet
  C->>P: HTTPS api.anthropic.com, proxy CA trusted inside
  P->>P: policy check, TLS terminated
  P->>P: credential header injected
  P->>I: new TLS session upstream
  I-->>P: response
  P-->>C: response
  C->>P: HTTPS example.com
  P-->>C: 403, counted per host in the network log
```

The squid path never decrypts anything and logs every CONNECT. Docker's path
decrypts everything, which is what lets it inject the credential so the token
never exists inside, and logs counts per host rather than requests.

## 4. The commit workflow and the .git hazard

```mermaid
sequenceDiagram
  participant Cl as Claude inside
  participant G as .git, shared with the host
  participant H as you on the host
  Cl->>G: git commit, identity injected from the host
  H->>G: git log shows it immediately
  H->>H: git push with your own credentials
  Note over Cl,G: hazard: a hook or a config key written here runs on the host at your next git command
  Note over G: moat: hooks are an empty read-only tmpfs, config a read-only bind
  Note over G: moat-docker: writable, the sandbox refuses mounts over the workspace
```

This is the one place where moat is stronger for the commit-inside,
push-on-host workflow. In the Docker sandbox the only thing between Claude
and a planted hook is the permission prompt, plus the Edit and Write deny
rules under `.git` in settings.json.

## 5. Side by side

| | moat | moat-docker |
| --- | --- | --- |
| Boundary | container, cap-drop ALL, shared VM kernel | microVM with its own kernel |
| First escape lands in | Docker Desktop VM | this project's VM |
| Egress enforcement | squid sidecar on an internal network | Docker host proxy |
| Traffic visibility | hostnames only, no decryption | full decryption via injected CA |
| Allowlist | yours alone | yours, Docker's built-ins blocked by port |
| Claude credential | host OAuth token as env var, per attach | `/login` once, token kept on the host |
| Token reachable by a prompt-injected command | yes, usable only against allowed hosts | no |
| `.git/hooks` and `.git/config` | masked read-only | writable |
| Root inside | none | removed by the script, Docker grants it |
| Permissions | prompts, git push and WebSearch denied | same settings applied |
| Audit | every CONNECT plus every tool call | per-host counters plus every tool call |
| Startup | seconds, instant reuse | about 20 s to create, seconds to reuse |
| Dependencies | Docker Desktop | Docker Desktop with the sandbox plugin |
| Lines you own | about 600 | about 200 on top of Docker's product |

## 6. The pattern moat replaces: a firewall inside the container

```mermaid
flowchart LR
  subgraph dc["devcontainer with in-container iptables"]
    c["claude<br/>node user with passwordless sudo<br/>NET_ADMIN and NET_RAW"]
    fw["iptables in the same container<br/>opt-in, never ran by default<br/>IPs resolved once at start"]
  end
  c -. "sudo iptables -F" .-> fw
  c --> net["internet"]
```

This is the reference devcontainer pattern and what moat grew out of. The
enforcement point lives inside the thing being sandboxed, so one command
removes it, and rules resolved from DNS at start go stale. Both designs above
move enforcement outside the agent's reach.

## 7. Which one when

- **Interactive work on private repos**, committing inside and pushing from
  the host: moat. The `.git` masks, the allowlist you own and the
  request-level log matter more than the VM boundary while you are watching.
- **Unattended runs, untrusted code, tasks that need Docker or root inside,
  or a credential the agent must use but must never hold**: moat-docker.
  The VM boundary and proxy-side credentials are exactly what you want when
  nobody is watching or the code is not yours.
