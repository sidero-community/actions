# talos2disk action design

Date: 2026-09-12 (revised the same day: schematic ownership moved to CABPT).
Status: approved design; the first implementation landed on `main`, this
revision is awaiting its implementation plan.

Repositories touched by this revision: `sidero-community/actions` (this
repository), the `cluster-api-runtime-extensions-tinkerbell` chart (the
install Workflow Template), and reverts in `cluster-api-provider-tinkerbell`
and `cluster-api-runtime-extensions-tinkerbell`. The schematic resolution that
this action relies on is designed in the CABPT repository:
`docs/design/2026-09-12-image-factory-schematic.md`.

## Goal

Replace the four-action Talos install sequence (image2disk, taloscmdline,
talosmeta, reboot) with two actions: `talos2disk` followed by the existing
waitdaemon reboot. `talos2disk` runs inside the OSIE on the target machine and

1. reads the machine's Hardware object, handed to it by the Workflow Template,
2. selects the install disk with a Talos-shaped disk selector,
3. reads the Image Factory schematic ID and Talos version that CABPT rendered
   into `machine.install.image` of the machine configuration,
4. streams the Factory's raw metal image for that schematic onto the disk,
5. stamps kernel arguments into the UKI command lines,
6. writes the Talos network configuration to the META partition,
7. exits. Rebooting stays a separate action.

Every setting falls through a fixed chain: explicit environment, then the
Talos machine configuration in `spec.userData`, then the rest of the Hardware
object, then a default. The action never talks to the Kubernetes API, never
registers schematics and never inspects the machine beyond its disks.

The repository also hosts `taloscmdline` and `talosmeta` as standalone actions
with their existing contracts, built from the same shared packages under
`pkg/`. It carries the build, lint and release scaffolding of
`tinkerbell/actions`.

## Decisions taken during design

| Question | Decision |
| --- | --- |
| Who determines the schematic | CABPT, from `spec.imageFactory` on the TalosConfig, rendered as `machine.install.image = <factory host>/metal-installer/<id>:<version>`. The action only consumes that reference. Hardware detection, Factory registration and the Hardware write-back from the first revision are removed. |
| Talos version source | `TALOS_VERSION` env, else the tag of `machine.install.image`, else `metadata.instance.operating_system.version`, else the `talos.tinkerbell.org/contract` annotation. Full versions are used exactly; a bare minor resolves to the newest non-broken patch via the Factory. |
| Schematic ID source | `SCHEMATIC_ID` env, else the ID in `machine.install.image` when it is a Factory installer reference, else `metadata.instance.operating_system.slug`. Nothing else: the action cannot invent a schematic. |
| Factory host | `FACTORY_URL` env, else `https://` plus the host of the installer reference, else `https://factory.talos.dev`. |
| Disk selector | Talos `machine.install.diskSelector` shape everywhere: `DISK_SELECTOR` env, else the config's `diskSelector`, else the config's `disk` path, else `spec.disks[0].device`, else `{ "size": ">= 100GB" }`. Multiple matches pick the first by device name, as Talos does. |
| Hardware data | Passed by the Template as one `HARDWARE` env var holding the Hardware object as JSON (`.hardware | toJson`), including `spec.userData`. No metadata-service round trip and no tootles change. |
| Kernel arguments | Stamped into the UKI after the write, merging the config's `machine.install.extraKernelArgs` under `KERNEL_ARGS`. |
| Structure | Three actions, shared code under `pkg/`. |

## talos2disk contract

### Environment

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `HARDWARE` | yes | | The Hardware object as JSON with `metadata` (name, namespace, labels, annotations), `spec` and `status`. Rendered by the Template from `.hardware`. |
| `SCHEMATIC_ID` | no | | Factory schematic ID (64 hex characters). Overrides the reference in the machine configuration and the Hardware. |
| `TALOS_VERSION` | no | | Full version (`v1.14.2`) or bare minor (`v1.14`). Overrides every version carried by the Hardware. |
| `DISK_SELECTOR` | no | | Talos `machine.install.diskSelector` document, YAML or JSON. Overrides the config and the Hardware. |
| `KERNEL_ARGS` | no | empty | Whitespace-separated arguments merged into every `EFI/Linux/Talos-*.efi` command line after the config's `extraKernelArgs`. `talos.config=…`, `net.ifnames=0`, consoles. |
| `FACTORY_URL` | no | derived | Image Factory base URL. Defaults to the host of the installer reference, else `https://factory.talos.dev`. |
| `NETWORK_CONFIG` | no | | Complete Talos network configuration document written verbatim to META key `0xa`. |
| `LINK_NAMING` | no | inferred | `kernel` or `predictable`. When unset, `kernel` if the final command line contains `net.ifnames=0`, else `predictable`. |
| `STRIP_SIGNATURE` | no | `false` | Allow rewriting an Authenticode-signed UKI. |
| `RETRY_DURATION_MINUTES` | no | `10` | Window for retrying the image download with exponential backoff. `0` disables retries. |
| `DRY_RUN` | no | `false` | Resolve everything, print the plan, exit 0 without touching the disk. |

Removed from the first revision: `EXTENSIONS`, `OVERLAY`, `NVIDIA_EXTENSIONS`,
`KUBECONFIG`. Extensions, overlays and kernel modules are declared on the
TalosConfigTemplate and reach the machine through the configuration CABPT
renders.

### The Hardware object and the fall-through

| Path | Used for |
| --- | --- |
| `spec.userData` | The Talos machine configuration: `machine.install.image` (schematic and version), `machine.install.disk`, `machine.install.diskSelector`, `machine.install.extraKernelArgs`, `machine.network.hostname`, `machine.network.nameservers`, `machine.time.servers`. |
| `spec.interfaces[].dhcp.*` | Network configuration: links, addresses, routes, VLANs, DHCP-supplied hostname, resolvers and time servers. |
| `spec.metadata.instance.hostname`, `.ips[]`, `.operating_system.{slug,version}` | Hostname and external IPs; schematic and version fallbacks. |
| `metadata.annotations["talos.tinkerbell.org/contract"]` | Bare-minor version fallback. |
| `spec.disks[].device` | Disk fallback before the default selector. |
| `status.attributes.outOfBand.blockDevices[]` | Diagnostics only: a warning when the chosen disk's serial is not in the inventory. |

`spec.userData` is parsed as multi-document YAML; the document with
`version: v1alpha1` and a `machine` key is the machine configuration. Unknown
fields are ignored. An absent or empty `userData` leaves the configuration
layer empty; a present but unparsable `userData` is an error. The
configuration is never logged.

| Setting | Chain |
| --- | --- |
| Schematic ID | `SCHEMATIC_ID`; the ID in `machine.install.image` (`<host>/metal-installer/<id>:<tag>` or `<host>/installer/<id>:<tag>`); `metadata.instance.operating_system.slug`. None is an error. |
| Version | `TALOS_VERSION`; the tag of `machine.install.image` when it looks like a Talos version; `metadata.instance.operating_system.version`; the contract annotation. None is an error. |
| Factory URL | `FACTORY_URL`; `https://<host of machine.install.image>`; `https://factory.talos.dev`. |
| Install disk | `DISK_SELECTOR`; `machine.install.diskSelector`; `machine.install.disk` when it starts with `/dev/` (placeholders such as `DISK_ID` skipped); `spec.disks[0].device`; `{ "size": ">= 100GB" }`. |
| Kernel arguments | `machine.install.extraKernelArgs` merged first, `KERNEL_ARGS` merged last. |
| Hostname in META | `machine.network.hostname`; `metadata.instance.hostname`; the first `dhcp.hostname`. |
| Resolvers in META | `machine.network.nameservers`; the union of `dhcp.name_servers`. |
| Time servers in META | `machine.time.servers`; the union of `dhcp.time_servers`. |
| Interfaces in META | `spec.interfaces[].dhcp` only. |
| Whole META document | `NETWORK_CONFIG` replaces all of the above. |

### Pipeline

Every remote call and every decision happens before the first byte is written.

1. Parse the environment and `HARDWARE`, including the machine configuration in `userData`. Unparsable input fails immediately.
2. Enumerate whole disks and evaluate the disk chain. Log every eligible disk, the candidates, and the chosen disk. More than one candidate logs a warning naming them all. A chosen disk not listed in `spec.disks` or absent from the out-of-band inventory logs a warning.
3. Resolve the schematic ID and the version through their chains. A bare minor calls `GET /versions` on the Factory.
4. Build the image URL `<FACTORY_URL>/image/<id>/<version>/metal-<arch>.raw.zst` (arch from the action's own `GOARCH`) and log the plan. `DRY_RUN` exits here.
5. Stream the image onto the disk: HTTP GET following redirects, zstd decompression, progress every 5 s, `fsync`, `BLKRRPART`. Retry from the beginning within the retry window.
6. Re-read the GPT and locate the `EFI` and `META` partitions by label. Fail if either is missing.
7. If the merged kernel arguments are non-empty, obtain the EFI partition device node, mount it as vfat, and merge them into every matching UKI.
8. Build the network document and write META key `0xa`. When no document results, log a warning and skip.
9. Log a summary: disk, schematic ID, version, image URL, resulting command lines, network document. Exit 0.

## Disk selection

Semantics mirror Talos' `InstallDiskSelector` and its `DiskMatchExpression`.
Whole disks are the entries of `/sys/block`, probed with go-blockdevice v2.
Every match requires a non-empty transport, not read-only, not CD-ROM.
`size` takes `[op]<size>` with `>=`, `<=`, `>`, `<`, `==` (default `==`) and
go-humanize sizes; `model`, `serial`, `modalias`, `uuid`, `wwid`, `busPath`
are globs; `type` maps `nvme`/`sd` to transports and `hdd`/`ssd` to
rotational; `name` is rejected. Candidates are ordered by device name and the
first is chosen. Device paths (`machine.install.disk`, `spec.disks[0]`) are
resolved through symlinks and matched on `dev_path`.

## Image write, partition access, UKI and META

Unchanged from the first revision: `pkg/image` streams and decodes zstd with
retry; `pkg/partition` locates partitions by label and waits for or creates
the EFI device node; `pkg/cmdline` mounts vfat and merges arguments into
every UKI (signed UKIs refused unless `STRIP_SIGNATURE`); `pkg/talosnet` and
`pkg/meta` build and write the network document with the hostname, resolver
and time server overrides from the configuration.

## Workflow Template in the runtime-extensions chart

```yaml
actions:
  - name: "Install Talos"
    image: __ACTIONS_REPOSITORY__/talos2disk:__ACTIONS_TAG__
    timeout: 9600
    namespaces:
      network: host
    environment:
      HARDWARE: {{ dict "metadata" (pick .hardware.metadata "name" "namespace" "labels" "annotations") "spec" .hardware.spec "status" (dig "status" (dict) .hardware) | toJson | quote }}
      KERNEL_ARGS: "console=tty0 console=ttyAMA0,115200 net.ifnames=0 talos.config=__TOOTLES_USER_DATA_URL__"
  - name: "reboot"
    # unchanged waitdaemon action
```

No shared volume and no kubeconfig. `.hardware` is the whole Hardware object
keyed by JSON field names; `pick` drops `managedFields`. tink caps a rendered
Template at 256 KiB and Linux caps a single environment string at 128 KiB;
a Hardware with a Talos configuration and a Redfish inventory is tens of KiB.
The machine configuration in `userData` is already served to the node by
tootles; carrying it in the Workflow environment exposes it to the same
namespace and RBAC audience. The action never logs it.

## Repository layout

| Path | Contents |
| --- | --- |
| `pkg/hardware` | Hardware object types and JSON parsing; `Fetch` for talosmeta. |
| `pkg/talosconfig` | Minimal v1alpha1 parser: `machine.install`, `machine.network.hostname`/`nameservers`, `machine.time.servers`, installer reference and tag. |
| `pkg/talosnet`, `pkg/meta` | Network document with overrides; META tag read and write. |
| `pkg/uki`, `pkg/kargs`, `pkg/cmdline`, `pkg/partition` | UKI rewrite, argument merging, mount-and-edit, partition lookup and nodes. |
| `pkg/disks` | Enumeration, prober, Talos-shaped selector, selection. |
| `pkg/factory` | Installer reference parsing, image URL, `GET /versions`, version resolution. No schematic registration. |
| `pkg/image` | Streaming download with zstd, progress, sync, partition re-read, retry. |
| `talos2disk/`, `taloscmdline/`, `talosmeta/` | Action mains, Dockerfiles, READMEs. |

Removed by this revision: `pkg/detect`, `pkg/kube`, `talosconfig.Patch`/`RenderPatch`,
`factory.Build`/`CreateSchematic`/`GetSchematic`/`MissingExtensions`, and the
`k8s.io/*` dependencies.

## Failure handling

Before the write, every failure exits non-zero with the disk untouched:
missing or unparsable `HARDWARE`, unparsable `userData`, unparsable selector,
no matching disk, no schematic ID, no or unresolvable version, Factory
`/versions` failure. `DRY_RUN` stops at the same point on success. After the
write, a partition-table, UKI or META failure exits non-zero and leaves a
written but unfinished disk; re-running the action is safe because every step
rewrites its output in full.

## Testing

Unit tests per package as in the first revision, minus the removed packages.
The talos2disk pipeline test asserts that the image URL is built from the
schematic ID and tag in `machine.install.image`, that the Factory receives no
`POST`, that no Hardware patch happens, and that `SCHEMATIC_ID` and
`operating_system.slug` take their places in the chain. Manual validation:
`DRY_RUN` on a real machine, then the loop-device and QEMU/OVMF recipe.

## Out of scope and follow-ups

- Schematic declaration, extension validation and version pinning live in CABPT (`docs/design/2026-09-12-image-factory-schematic.md`).
- The runtime-extensions resolver and the Workflow gate stay untouched; `operating_system.slug`/`version` are only a fallback here.
- Secure Boot images, image checksum verification, and OCI image sources are not handled.
