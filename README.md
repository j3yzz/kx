# kx

A faster CLI for Kubernetes. Work with an app by its name, not by a pod name:

```console
$ kx exec payments                    # shell in a running pod of "payments"
$ kx forward mysql 3306               # localhost:3306 -> mysql/mysql-0:3306, reconnects if the pod restarts
$ kx secrets payments                 # which Secrets it uses, values masked
```

kx talks to the Kubernetes API directly (client-go). It does not need the `kubectl` binary.

## Install

```sh
# Homebrew
brew install j3yzz/kx/kx

# krew (kubectl plugin)
kubectl krew install kx

# from source (Go 1.26+)
go install github.com/j3yzz/kx@latest
```

Release binaries for macOS and Linux (amd64, arm64) are on the [releases page](https://github.com/j3yzz/kx/releases).

Shell completion for app names, namespaces, contexts and containers:

```sh
source <(kx completion zsh)    # or bash; fish: kx completion fish | source
```

## How an app name is resolved

`kx <command> payments` looks in the current namespace, in this order:

1. `-l <selector>`, if given.
2. A Deployment, StatefulSet or DaemonSet named `payments`, using its selector.
3. Pods labelled `app.kubernetes.io/name=payments`, `app=payments`, or `app.kubernetes.io/instance=payments` (a Helm release).

It picks a Running, Ready pod, newest first. If a Helm release has several components (web, worker, ...), kx stops and asks you to choose with `--pick` or `-l`. If the app is not in this namespace, the error says where it does exist.

Omit the app name on a terminal and you get a type-to-filter list of the namespace's apps.

## Commands

| Command | What it does |
|---|---|
| `kx exec <app> [-- cmd]` (`sh`) | Shell or command in a pod. Starts `bash`, or `sh` if there is none. The remote exit code becomes kx's. |
| `kx forward <app> [ports]` (`pf`) | Port-forward. `3306` or `13306:3306`. A busy local port is replaced by a free one. Reconnects with backoff when the pod is replaced. |
| `kx tunnel [profile]` (`tun`) | Saved forward from the config, optionally starting a local client and stopping when it exits. |
| `kx secrets <app>` (`sec`) | Secrets the app uses and where each key is consumed. Masked unless `--reveal`. `--key K`, `--copy`, `-o env\|json`. |
| `kx secrets get <name>` | One Secret by name. |
| `kx secrets diff <app>` | Compare Secrets across namespaces or contexts without printing values. |
| `kx configmaps <app>` (`cm`) | Same for ConfigMaps. Values are shown. |
| `kx env <app>` | Effective environment of the pod, with the source of each variable. Secret values masked. |
| `kx logs <app>` | Logs of all pods, prefixed per pod. `-f`, `--since`, `--previous`, `--tail`. |
| `kx status <app>` | Replicas, restarts, last termination reason (e.g. `OOMKilled`), recent events. |
| `kx top <app>` | CPU and memory per pod against requests and limits. Needs metrics-server. |
| `kx debug <app>` | Ephemeral debug container for images with no shell. It stays until the pod is replaced. |
| `kx cp <app>:<path> <local>` | Copy files out of or into a pod (tar over exec; the container needs `tar`). |
| `kx run-all <app> -- cmd` | Run a command in every running pod, output prefixed by pod. Exits 1 if it failed anywhere. |
| `kx restart <app>` | Rolling restart. |
| `kx scale <app> N` | Set replicas. |

`restart` and `scale` (the only commands that change the cluster) need the exact workload name; kx never guesses through labels for them.

Global flags: `-n`, `--context`, `--kubeconfig`, `-o table|json`, `--no-color`, `-y/--yes`.

Exit codes: `0` ok, `1` general error, `2` app not found, `3` access denied.

## Safety

- **Secrets are masked** (`********`) unless you pass `--reveal`. Values never appear in error messages or the audit log.
- **Prod guard.** Contexts matching `prodContexts` print a red `context ...` line on every command and ask for confirmation before exec, forward, reveal, cp, restart and scale. In scripts, pass `--yes`; without a terminal and without `--yes`, kx refuses.
- **Non-default targets are announced.** If `-n` or `--context` differs from your kubeconfig defaults, the first line says where kx acts.
- **Permission pre-check.** Before acting, kx asks the API whether you may, and reports every missing permission at once (exit 3).
- **Audit log.** Every guarded action is appended as a JSON line to `$XDG_STATE_HOME/kx/audit.log` (default `~/.local/state/kx/audit.log`): time, user, context, namespace, action, target and outcome (`allowed` or `declined`).
- **Strict config.** Unknown keys are an error, so a typo such as `prodContext` cannot silently turn the guard off.

## Configuration

`~/.config/kx/config.yaml`. A missing file means defaults.

```yaml
prodContexts: ["*-prod", "prod-eu"]   # globs allowed
clipboardClearSeconds: 30              # --copy clears the clipboard after this; 0 = never
auditLog: true                         # false turns the audit log off

aliases:                               # kx exec pay  ->  app payments-web in namespace payments
  pay: {app: payments-web, namespace: payments}

tunnels:                               # kx tunnel db
  db:
    app: mysql
    namespace: data
    ports: ["13306:3306"]
    client: mysql -h {host} -P {port} -u root -p   # optional; {host}/{port} = first local port
```

## Known limits

- `logs -f` does not re-attach after a container restart inside the same pod.
- `--copy` clears the clipboard unconditionally after the delay, even if you copied something else meanwhile.
- The interactive picker covers apps only; choosing a pod uses a numbered list (`--pick`).
- `restart` does not wait for the rollout to finish.
- macOS and Linux only.

## Development

```sh
make build && make test
bash hack/kind-test.sh     # integration tests against a throwaway kind cluster (needs Docker)
```

The integration suite refuses to run unless `KX_TEST_KUBECONFIG` points at a `kind-` context.
