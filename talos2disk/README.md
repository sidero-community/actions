```
ghcr.io/sidero-community/actions/talos2disk:latest
```

This action installs [Talos Linux](https://www.talos.dev) from the
[Image Factory](https://factory.talos.dev) onto the machine it runs on. In one
step it selects the install disk, determines and registers the Factory
schematic, resolves the Talos version, streams the raw metal image onto the
disk, sets kernel arguments in the UKI, writes the network configuration to
the META partition, and records the resulting installer image and machine
configuration overrides on the Hardware object. Rebooting is left to the next
action.

```yaml
actions:
  - name: "Install Talos"
    image: ghcr.io/sidero-community/actions/talos2disk:latest
    timeout: 9600
    volumes:
      - /var/lib/tink/shared:/shared
    environment:
      HARDWARE: {{ dict "metadata" (pick .hardware.metadata "name" "namespace" "labels" "annotations") "spec" .hardware.spec "status" (dig "status" (dict) .hardware) | toJson | quote }}
      KERNEL_ARGS: "console=tty0 net.ifnames=0 talos.config=http://192.168.1.2:7080/2009-04-04/user-data"
      KUBECONFIG: /shared/kubeconfig
  - name: "reboot"
    image: ghcr.io/jacobweinstock/waitdaemon:0.4.3
    timeout: 90
    pid: host
    environment:
      IMAGE: alpine:3.22
      WAIT_SECONDS: 10
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    command: ["reboot", "-f"]
```

## Environment Variables

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `HARDWARE` | yes | | The Hardware object as JSON with `metadata` (name, namespace, labels, annotations), `spec` and `status`. Rendered by the Template from `.hardware`; see above. |
| `TALOS_VERSION` | no | | Full version (`v1.14.2`) or bare minor (`v1.14`). Overrides every version carried by the Hardware. |
| `DISK_SELECTOR` | no | | Talos `machine.install.diskSelector` document, YAML or JSON. Overrides the config and the Hardware. |
| `KERNEL_ARGS` | no | | Arguments merged into every `EFI/Linux/Talos-*.efi` command line after the config's `extraKernelArgs`. `talos.config=…` belongs here. |
| `EXTENSIONS` | no | | Comma-separated official extensions merged into the schematic. |
| `OVERLAY` | no | | `name@image`. Overrides the `talos.tinkerbell.org/overlay` annotation. |
| `NVIDIA_EXTENSIONS` | no | `siderolabs/nvidia-open-gpu-kernel-modules-production,siderolabs/nvidia-container-toolkit-production` | Extensions added when an NVIDIA GPU is detected. Empty adds none. |
| `FACTORY_URL` | no | `https://factory.talos.dev` | Image Factory base URL. |
| `NETWORK_CONFIG` | no | | Complete Talos network configuration written verbatim to META key `0xa`. |
| `LINK_NAMING` | no | inferred | `kernel` or `predictable`; inferred from `net.ifnames=0` in the final command line. |
| `STRIP_SIGNATURE` | no | `false` | Allow rewriting an Authenticode-signed UKI. |
| `RETRY_DURATION_MINUTES` | no | `10` | Window for retrying the image download. `0` disables retries. |
| `KUBECONFIG` | no | | Path of a kubeconfig able to `get` and `patch` `hardware.tinkerbell.org`. When set, the Hardware is patched after the install; when unset, the write-back is skipped with a warning. |
| `DRY_RUN` | no | `false` | Resolve everything, print the plan, exit 0 without touching the disk or the Hardware. |

## How settings are chosen

Every setting falls through the same chain: environment, then the Talos
machine configuration in `spec.userData`, then the rest of the Hardware, then
detection, then a default.

| Setting | Chain |
| --- | --- |
| Install disk | `DISK_SELECTOR`; `machine.install.diskSelector`; `machine.install.disk` when it starts with `/dev/`; `spec.disks[0].device`; `{ "size": ">= 100GB" }`. Several matches pick the first by device name, as Talos does. |
| Version | `TALOS_VERSION`; the tag of `machine.install.image`; `metadata.instance.operating_system.version`; the `talos.tinkerbell.org/contract` annotation. |
| Kernel arguments | `machine.install.extraKernelArgs`, then `KERNEL_ARGS` on top. |
| Extensions | Detected (NVMe → `nvme-cli`, Intel/AMD CPU → microcode, AMD/Intel/NVIDIA GPU → `amdgpu`/`i915`/`NVIDIA_EXTENSIONS`), plus the `talos.tinkerbell.org/system-extensions` annotation, plus `EXTENSIONS`. |
| META hostname, resolvers, time servers | `machine.network.hostname`, `machine.network.nameservers`, `machine.time.servers`; then the DHCP values from `spec.interfaces`. |

The disk selector accepts the Talos fields `size`, `model`, `serial`,
`modalias`, `uuid`, `wwid`, `busPath` and `type` (`ssd`, `hdd`, `nvme`,
`sd`), with the same implicit filters Talos applies (a real transport, not
read-only, not a CD-ROM). `name` is rejected, as in Talos.

## What is written back

With `KUBECONFIG` set, after the disk is complete the Hardware receives one
merge patch: the annotations `talos.tinkerbell.org/installer-image`
(`<factory>/metal-installer/<schematic>:<version>`),
`talos.tinkerbell.org/config-patch` (the overrides as YAML) and, when the
Hardware carried a machine configuration,
`talos.tinkerbell.org/userdata-owner: talos2disk` together with `spec.userData`
rewritten so `machine.install.image` names the installed schematic and, for an
NVIDIA GPU, `machine.kernel.modules` lists `nvidia`, `nvidia_uvm`,
`nvidia_drm` and `nvidia_modeset`. The machine configuration is never logged.

## Verifying

```
talosctl -n <node> get extensions          # the "schematic" extension version is the schematic ID
talosctl -n <node> read /proc/cmdline
talosctl -n <node> get meta 0x0a -o yaml
kubectl -n tinkerbell get hardware <name> -o jsonpath='{.metadata.annotations}'
```
