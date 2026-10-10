# windy-node

A small agent that tells **Windy Mind** what a machine is doing: its vitals and the AI models it is running. You add the machine to your Windy account, approve it, and from then on you can see it, and later switch things on it off, from Windy.

**Status: v0.1 (Linux + NVIDIA).** Enrolment works against Mind today. Readings wait on disk until Mind's readings route is live (contract `node.v1` 1.1.0); nothing is lost while they wait (up to 24 hours).

## What it does, and what it never does

| It does | It never does |
|---|---|
| Reads CPU, memory and network counters from `/proc`, every 2 s, and sends a summary every 15 s | Open a port. It only makes outbound HTTPS calls to Mind |
| Reads GPU load, VRAM, temperature and power from `nvidia-smi` | Run a shell, or any command Mind sends that is not on a short fixed list (commands arrive in a later version, signed by Mind) |
| Asks the local Ollama which models are loaded (`/api/ps`), including how much spilled from the GPU onto the CPU | Read your files, your process list or anyone's addresses |
| Groups GPU processes into engine classes (ollama, llama.cpp, vllm, other) with the memory they hold | Send a process name, a PID or a command line |

Your node token is stored in a file only root (or you) can read, and it is only ever sent in a request header, never in a URL.

## Install (Linux)

```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin windy-node
sudo install -m 0755 windy-node-linux-amd64 /usr/local/bin/windy-node      # check SHA256SUMS first
sudo install -d -o windy-node -g windy-node -m 0700 /var/lib/windy-node
# the one-time code Windy shows you after you sign in (10 minutes, single use):
sudo -u windy-node env WINDY_NODE_DIR=/var/lib/windy-node windy-node enrol --code WN-ABCD-1234 --label my-machine
# approve the machine in Windy, then:
sudo install -m 0644 deploy/windy-node.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now windy-node
sudo -u windy-node env WINDY_NODE_DIR=/var/lib/windy-node windy-node status
```

It runs as the unprivileged `windy-node` user with no capabilities and a read-only system. `windy-node collect --once` prints one reading on screen and sends nothing, so you can see exactly what would be shared.

**Opt out of commands:** `sudo -u windy-node touch /var/lib/windy-node/no-commands`. Every command is refused while it exists.

**Where it connects:** only the Windy Mind URL it was enrolled with (default `https://api.windymind.ai`), plus the local Ollama at `127.0.0.1:11434` (or `$OLLAMA_HOST`). It listens on nothing.

**Remove it:** `sudo systemctl disable --now windy-node`, remove `/usr/local/bin/windy-node` and `/var/lib/windy-node`, then remove the machine in Windy (its token stops working at once).

## Build

```sh
make test        # go vet + unit tests
make dist        # static linux/amd64 + linux/arm64 binaries and SHA256SUMS
```

## Licence

Apache-2.0. See [LICENSE](LICENSE).
