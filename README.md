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

**Opt out of commands:** create an empty file named `no-commands` in the config directory (`/etc/windy-node/` for the service). Every command is refused while it exists.

## Install (Linux)

```sh
sudo install -m 0755 windy-node-linux-amd64 /usr/local/bin/windy-node
sudo windy-node enrol --code ABCD-1234     # the one-time code Windy shows you after you sign in (10 minutes)
# approve the machine in Windy, then:
sudo install -m 0644 deploy/windy-node.service /etc/systemd/system/
sudo systemctl enable --now windy-node
windy-node status
```

`windy-node collect --once` prints one reading on screen and sends nothing, so you can see exactly what would be shared.

**Remove it:** `sudo systemctl disable --now windy-node`, then remove the machine in Windy (its token stops working at once).

## Build

```sh
make test        # go vet + unit tests
make dist        # static linux/amd64 + linux/arm64 binaries and SHA256SUMS
```

## Licence

Apache-2.0. See [LICENSE](LICENSE).
