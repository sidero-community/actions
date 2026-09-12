# talos2disk action design

Date: 2026-09-12. Status: approved design, awaiting implementation plan.

Repositories touched: `sidero-community/actions` (this repository, new) and the
`cluster-api-runtime-extensions-tinkerbell` chart (the install Workflow
Template). Neither the `tinkerbell-community/tinkerbell` fork nor the
`tinkerbell-community/actions` fork is changed.

## Goal

Replace the four-action Talos install sequence (image2disk, taloscmdline,
talosmeta, reboot) with two actions: `talos2disk` followed by the existing
waitdaemon reboot. `talos2disk` runs inside the OSIE on the target machine and

1. reads the machine's Hardware object, handed to it by the Workflow Template,
2. selects the install disk with a Talos-shaped disk selector,
3. determines the Talos Image Factory schematic from what it can see on the
   machine plus the Hardware annotations, and registers it with the Factory,
4. resolves the Talos version,
5. streams the Factory's raw metal image onto the disk,
6. stamps kernel arguments into the UKI command lines,
7. writes the Talos network configuration to the META partition,
8. exits. Rebooting stays a separate action.

Every setting falls through a fixed chain: explicit environment, then the
Talos machine configuration in `spec.userData`, then the rest of the Hardware
object, then what the action detects on the machine, then a default.

The repository also hosts `taloscmdline` and `talosmeta` as standalone actions
with their existing contracts, built from the same shared packages under
`pkg/`. It carries the build, lint and release scaffolding of
`tinkerbell/actions` so the three vendor-specific actions are built and
published the same way upstream actions are.

## Decisions taken during design

| Question | Decision |
| --- | --- |
| Who determines the schematic | The action, always, from machine detection plus the Hardware annotations. It registers the schematic with the Factory itself. `machine.install.image` in the config contributes only its version tag. |
| Talos version source | `TALOS_VERSION` env, else the tag of `machine.install.image`, else `metadata.instance.operating_system.version`, else the `talos.tinkerbell.org/contract` annotation. Full versions are used exactly; a bare minor resolves to the newest non-broken patch via the Factory. |
| Disk selector | Talos `machine.install.diskSelector` shape everywhere: `DISK_SELECTOR` env, else the config's `diskSelector`, else the config's `disk` path, else `spec.disks[0].device`, else `{ "size": ">= 100GB" }`. Multiple matches pick the first by device name, as Talos does. |
| Detection rules | NVMe present, CPU vendor, display-class PCI vendor, architecture. Local probes first, `status.attributes.outOfBand` as fallback. |
| Hardware data | Passed by the Template as one `HARDWARE` env var holding the Hardware object as JSON (`.hardware | toJson`), including `spec.userData` and `status.attributes.outOfBand`. No metadata-service round trip and no tootles change. |
| Kernel arguments | Stamped into the UKI after the write, merging the config's `machine.install.extraKernelArgs` under `KERNEL_ARGS`. The schematic carries no `extraKernelArgs`. |
| Structure | New repository, three actions, shared code under `pkg/`. |

## talos2disk contract

### Environment

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `HARDWARE` | yes | | The Hardware object as JSON with `metadata` (name, namespace, labels, annotations), `spec` and `status`. Rendered by the Template from `.hardware`. |
| `TALOS_VERSION` | no | | Full version (`v1.14.2`) or bare minor (`v1.14`). Overrides every version carried by the Hardware. |
| `DISK_SELECTOR` | no | | Talos `machine.install.diskSelector` document, YAML or JSON. Overrides the config and the Hardware; see the fall-through below. |
| `KERNEL_ARGS` | no | empty | Whitespace-separated arguments merged into every `EFI/Linux/Talos-*.efi` command line after the config's `extraKernelArgs`. Deploy-level values belong here: `talos.config=…`, `net.ifnames=0`, consoles. |
| `EXTENSIONS` | no | | Comma-separated official extensions merged into the schematic. |
| `OVERLAY` | no | | `name@image`. Overrides the `talos.tinkerbell.org/overlay` annotation. |
| `FACTORY_URL` | no | `https://factory.talos.dev` | Image Factory base URL. |
| `NETWORK_CONFIG` | no | | Complete Talos network configuration document written verbatim to META key `0xa`, bypassing the Hardware mapping. |
| `LINK_NAMING` | no | inferred | `kernel` or `predictable`. When unset, `kernel` if the final command line contains `net.ifnames=0`, else `predictable`. |
| `STRIP_SIGNATURE` | no | `false` | Allow rewriting an Authenticode-signed UKI; the signature is removed. |
| `RETRY_DURATION_MINUTES` | no | `10` | Window for retrying the image download with exponential backoff. `0` disables retries. |
| `DRY_RUN` | no | `false` | Resolve everything, print the plan, exit 0 without touching the disk. |

### The Hardware object and the fall-through

The action reads these parts of `HARDWARE`:

| Path | Used for |
| --- | --- |
| `metadata.annotations["talos.tinkerbell.org/system-extensions"]` | Per-machine extensions, comma separated. |
| `metadata.annotations["talos.tinkerbell.org/overlay"]` | Image Factory overlay, `name@image`. |
| `metadata.annotations["talos.tinkerbell.org/contract"]` | Bare-minor version fallback. |
| `spec.userData` | The Talos machine configuration: `machine.install.disk`, `machine.install.diskSelector`, `machine.install.image`, `machine.install.extraKernelArgs`, `machine.network.hostname`, `machine.network.nameservers`, `machine.time.servers`. |
| `spec.interfaces[].dhcp.*` | Network configuration: links, addresses, routes, VLANs, and DHCP-supplied hostname, resolvers and time servers. |
| `spec.metadata.instance.hostname`, `.ips[]`, `.operating_system.version` | Hostname and external IPs; version fallback. |
| `spec.disks[].device` | Disk fallback before the default selector. |
| `status.attributes.outOfBand.cpu.sockets[].vendor`, `.gpuDevices[].vendor`, `.blockDevices[]` | Detection fallback when the local probe reports nothing; diagnostics otherwise. |

`spec.userData` is parsed as multi-document YAML; the document with
`version: v1alpha1` and a `machine` key is the machine configuration. Unknown
fields are ignored, so the parser is a small local type, not the Talos
machinery module. An absent or empty `userData` leaves the configuration layer
empty and the chain falls through. A present but unparsable `userData` is an
error, because a node that cannot read its own configuration should not be
installed. The configuration is never logged; only the individual values the
action uses appear in the output.

Per setting, highest precedence first:

| Setting | Chain |
| --- | --- |
| Install disk | `DISK_SELECTOR`; `machine.install.diskSelector`; `machine.install.disk` when it starts with `/dev/` (placeholders such as `DISK_ID` are skipped); `spec.disks[0].device`; `{ "size": ">= 100GB" }`. |
| Version | `TALOS_VERSION`; the tag of `machine.install.image` when it looks like a Talos version; `metadata.instance.operating_system.version`; the contract annotation. |
| Kernel arguments | `machine.install.extraKernelArgs` merged first, `KERNEL_ARGS` merged last, so the environment wins on a shared key. |
| Extensions | Detection, plus the annotation, plus `EXTENSIONS`. Additive. |
| Overlay | `OVERLAY`; the annotation. |
| Hostname in META | `machine.network.hostname`; `metadata.instance.hostname`; the first `dhcp.hostname`. |
| Resolvers in META | `machine.network.nameservers`; the union of `dhcp.name_servers`. |
| Time servers in META | `machine.time.servers`; the union of `dhcp.time_servers`. |
| Interfaces in META | `spec.interfaces[].dhcp` only. `machine.network.interfaces` is not mirrored into META; Talos applies it itself once it has fetched the configuration. |
| Whole META document | `NETWORK_CONFIG` replaces all of the above. |

### Pipeline

Every remote call and every decision happens before the first byte is written.

1. Parse the environment and `HARDWARE`, including the machine configuration in `userData`. Unparsable input fails immediately.
2. Enumerate whole disks and evaluate the selected disk chain. Log every eligible disk with its properties, the candidates, and the chosen disk. More than one candidate logs a warning naming them all. A chosen disk that is not listed in `spec.disks` or in `status.attributes.outOfBand.blockDevices` logs a warning.
3. Detect machine facts and build the schematic. `POST /schematics`; log the canonical body the Factory returns and its ID. When `machine.install.image` names a different Factory schematic, fetch it with `GET /schematics/:id` and log the extensions the machine's schematic has that the configuration's lacks, so the operator knows what an upgrade through the configured installer would drop.
4. Resolve the version. Bare minors call `GET /versions`.
5. Build the image URL `<FACTORY_URL>/image/<id>/<version>/metal-<arch>.raw.zst` and log the full plan. `DRY_RUN` exits here.
6. Stream the image onto the disk: HTTP GET following redirects, zstd decompression, progress every 5 s, `fsync`, `BLKRRPART`. Retry from the beginning within the retry window.
7. Re-read the GPT and locate the `EFI` and `META` partitions by label. Fail if either is missing.
8. If the merged kernel arguments are non-empty, obtain the EFI partition device node, mount it as vfat, and merge them into every matching UKI.
9. Build the network document and write META key `0xa`. When no document results (no `NETWORK_CONFIG` and no interface with a static address), log a warning and skip.
10. Log a summary: disk, schematic ID and extensions, version, image URL, resulting command lines, network document. Exit 0.

## Disk selection

Semantics mirror Talos' `InstallDiskSelector` and its `DiskMatchExpression`.

**Enumeration.** Whole disks are the entries of `/sys/block`. Properties come from go-blockdevice v2 (`block.NewFromPath(...).GetProperties()`, `GetSize()`, `IsCD()`, `IsReadOnly()`), the same library Talos uses: device path, size, transport, rotational, read-only, CD-ROM, model, serial, modalias, UUID, WWID, bus path.

**Implicit filters.** Every match additionally requires a non-empty transport (this excludes loop, ram, zram, device-mapper and md devices), not read-only, and not CD-ROM.

**Fields.**

| Field | Match |
| --- | --- |
| `size` | `[op]<size>` with `op` in `>=`, `<=`, `>`, `<`, `==`; no operator means `==`. Size parsed by `go-humanize` `ParseBytes`: decimal `GB`/`TB` and binary `GiB`/`TiB`. |
| `model`, `serial`, `modalias`, `uuid`, `wwid`, `busPath` | Glob with `*` (`ryanuber/go-glob`, as Talos). |
| `type` | `nvme`: transport `nvme`. `sd`: transport `mmc`. `hdd`: rotational. `ssd`: not rotational. |
| `name` | Rejected with "selector on name is not supported", as Talos rejects it. |

An empty document matches every eligible disk. Fields combine with AND.

**Device paths.** `machine.install.disk` and `spec.disks[0].device` are paths, not selectors. A path is resolved through symlinks (so `/dev/disk/by-id/...` works) and matched against `dev_path`, still subject to the implicit filters.

**Selection.** Candidates are ordered by device name ascending and the first is chosen, matching Talos' behaviour of taking the first matched disk. All candidates are logged; more than one raises a warning.

## Schematic

```yaml
customization:
  systemExtensions:
    officialExtensions:
      - <sorted, unique>
  bootloader: sd-boot        # arm64 only
overlay:                     # only when an overlay is configured
  name: <name>
  image: <image>
```

| Signal | Local source | Out-of-band fallback | Effect |
| --- | --- | --- | --- |
| Architecture | the action's own `GOARCH` | none | `metal-<arch>` image path; `bootloader: sd-boot` on arm64. |
| NVMe | any enumerated disk with transport `nvme` | `blockDevices[]` with an NVMe controller or drive type | `siderolabs/nvme-cli` |
| CPU vendor | `vendor_id` in `/proc/cpuinfo` | `cpu.sockets[].vendor` | Intel → `siderolabs/intel-ucode`; AMD → `siderolabs/amd-ucode`. arm64 has no `vendor_id` and gets no microcode extension. |
| Display-class PCI device | `/sys/bus/pci/devices/*/class` starting `0x03`, with `vendor` | `gpuDevices[].vendor` | AMD (`0x1002`) → `siderolabs/amdgpu`; Intel (`0x8086`) → `siderolabs/i915`. NVIDIA (`0x10de`) is logged and not added, since its extension variants are a deployment choice. |
| Explicit | `talos.tinkerbell.org/system-extensions` annotation and `EXTENSIONS` | | merged |
| Overlay | `OVERLAY`, else the `talos.tinkerbell.org/overlay` annotation | | `overlay.name` and `overlay.image` |

The out-of-band fallback applies only when the local probe reports nothing for that signal; vendor strings from Redfish are matched case-insensitively on `Intel`, `AMD` and `Advanced Micro Devices`.

Extensions are deduplicated and sorted so identical machines yield identical schematics. The schematic carries no `extraKernelArgs`, `meta` or embedded configuration. The Factory ignores kernel arguments for the installer image anyway, and the UKI stamp covers the raw image. `machine.install.image` never selects the schematic; it is only compared against the computed one for the warning described in the pipeline.

Registration is `POST /schematics` with `Content-Type: application/yaml`. The response `{id, schematic}` is authoritative; the canonical body is logged. The ID is the SHA-256 of the canonical body, so re-registration is idempotent. The Factory validates extension names; an unknown name fails the action before any write.

## Version resolution

Order: `TALOS_VERSION`; the tag of `machine.install.image` (`<registry>/<path>:<tag>`, any registry, when the tag matches `vX.Y.Z[-pre]` or `vX.Y`); `metadata.instance.operating_system.version`; the `talos.tinkerbell.org/contract` annotation. No value at all is an error.

A full version `vX.Y.Z` or `vX.Y.Z-<pre>` is used exactly. A bare minor `vX.Y` resolves through `GET /versions` (which excludes versions the Factory marks broken): keep entries with prefix `vX.Y.`, drop prereleases (any `-`), pick the highest patch. No candidate is an error.

## Image write

The download follows redirects because the Factory may answer `302` to object storage. The body is decoded as a zstd stream and written to the whole-disk device; progress (bytes read and written) is logged every 5 s. On completion the device is synced and `BLKRRPART` is issued. Failures within `RETRY_DURATION_MINUTES` retry with exponential backoff (1 s doubling, capped at 30 s), rewriting from offset 0. zstd frame checksums detect corruption in transit; Factory `.sha256` sidecars are an enterprise feature and are not used.

## Partition access after the write

`pkg/partition` locates a partition by GPT label and derives its device node with the kernel naming rules (`/dev/sda1`, `/dev/nvme0n1p1`, `-part1` for `/dev/disk/by-*`). Because the write and the mount happen in one process, the node may not exist yet: the package waits up to 10 s for the host to create it, and otherwise reads `/sys/block/<disk>/<part>/dev` and creates a node with `mknod` under a private directory. META is written through the whole-disk device at the partition offset and needs no node.

## UKI command lines and META

The UKI step is the existing taloscmdline behaviour behind `pkg/cmdline`: mount the EFI partition as vfat, glob `EFI/Linux/Talos-*.efi`, merge the arguments into every `.cmdline` section (key=value replaces, bare flags append once), rewrite each UKI to a temporary file and rename it over the original. A signed UKI is refused unless `STRIP_SIGNATURE` is set. The merged argument set is the config's `extraKernelArgs` followed by `KERNEL_ARGS`.

The META step is the existing talosmeta behaviour with the fall-through above for hostname, resolvers and time servers: `NETWORK_CONFIG` is validated and written verbatim, otherwise `interfaces[]` and `metadata.instance` map onto the Talos metal platform network configuration on the `platform` layer, and the document is stored under key `0xa` with every other key preserved. Interface names resolve through `iface_name`, else the sysfs lookup by MAC with kernel or predictable naming. The naming mode is inferred from the final command line unless `LINK_NAMING` overrides it.

## Workflow Template in the runtime-extensions chart

`files/talos-install-template.yaml` becomes two actions. The reboot action is unchanged.

```yaml
actions:
  - name: "Install Talos"
    image: __ACTIONS_REPOSITORY__/talos2disk:__ACTIONS_TAG__
    timeout: 9600
    environment:
      HARDWARE: {{ dict "metadata" (pick .hardware.metadata "name" "namespace" "labels" "annotations") "spec" .hardware.spec "status" (dig "status" (dict) .hardware) | toJson | quote }}
      KERNEL_ARGS: "console=tty0 console=ttyAMA0,115200 net.ifnames=0 talos.config=__TOOTLES_USER_DATA_URL__"
  - name: "reboot"
    # unchanged waitdaemon action
```

`.hardware` is the whole Hardware object keyed by JSON field names, so it carries `spec.userData` and `status.attributes.outOfBand`; the legacy `.Hardware` struct carries neither status nor annotations and is not used. `pick` drops `managedFields` and the other bulky object metadata. `toJson | quote` yields a YAML double-quoted scalar, the form the talosmeta Template already used. tink caps a rendered Template at 256 KiB and Linux caps a single environment string at 128 KiB; a Hardware with a Talos configuration and a full Redfish inventory is tens of KiB, so both hold, and the action fails with a clear message if `HARDWARE` is missing or truncated. Host networking is no longer required; the chart may keep it for the download.

The machine configuration in `userData` already appears on the Hardware object and is served to the node by tootles; carrying it in the Workflow's action environment exposes it to the same namespace and RBAC audience. The action never logs it.

The Template body no longer references `operating_system`, so the content-addressed name changes and a new Template object ships, per the chart's existing rule. `template.actions.repository` defaults to `ghcr.io/sidero-community/actions`. The Workflow-CREATE gate stays; it now guarantees that a version reaches the machine through `operating_system.version` even when the configuration carries no installer reference.

## Repository layout and scaffolding

Module `github.com/sidero-community/actions`, Go 1.26.3 (required by go-adv). License MIT. The taloscmdline and talosmeta sources move from the `tinkerbell-community/actions` fork with import paths rewritten; they are this repository's author's own work. The image streaming code is written fresh rather than copied from Apache-2.0 image2disk.

Scaffolding adapted from `tinkerbell/actions`: `Makefile` (`ACTIONS := talos2disk taloscmdline talosmeta`, `BUILD_PLATFORM` from the host, `CONTAINER_REPOSITORY ?= ghcr.io/sidero-community/actions`, per-action `docker buildx` targets, `release-%` multi-arch push), `Rules.mk`, `Lint.mk`, `.golangci.yml`, `.yamllint`, `.gitignore`, `.github/workflows/ci.yml` (test, lint, build matrix over the three actions) and `release.yml` (build and push amd64 and arm64 to ghcr.io tagged with the Git SHA and `latest`), `README.md` with the action table, a short `CONTRIBUTING.md`. Each action has a `golang:1.26-alpine` to `scratch` Dockerfile that copies CA certificates, since the Factory is reached over HTTPS.

| Path | Origin | Contents |
| --- | --- | --- |
| `pkg/hardware` | moved from talosmeta, extended | Hardware object types: `metadata` (name, namespace, labels, annotations), `spec` (interfaces, disks, metadata.instance, userData), `status.attributes.outOfBand` (CPU sockets, GPU devices, block devices). `Fetch` stays for talosmeta's `MIRROR_HOST` path. |
| `pkg/talosconfig` | new | Minimal v1alpha1 machine configuration parser: multi-document split, `machine.install`, `machine.network.hostname`/`nameservers`, `machine.time.servers`, installer reference parsing. |
| `pkg/talosnet` | moved, extended | Network document mapping with hostname, resolver and time server overrides; the sysfs link namer. |
| `pkg/meta` | moved | META partition location and tag read/write via go-adv. |
| `pkg/uki`, `pkg/kargs` | moved | UKI `.cmdline` rewrite with the `ukitest` builder; argument merging. |
| `pkg/partition` | moved from efipart, extended | Locate by GPT label, device node naming, node wait and `mknod` fallback. |
| `pkg/cmdline` | extracted from taloscmdline main | Mount, glob, merge and rewrite behind a `Mounter` interface. |
| `pkg/disks` | new | Enumeration behind a `Prober` interface with the go-blockdevice implementation; the selector type, parsing and matching; path matching; selection. |
| `pkg/detect` | new | Architecture, CPU vendor, PCI display vendors, NVMe presence, with the out-of-band fallback; returns extension names. |
| `pkg/factory` | new | Schematic types in the Factory's field order; client for `POST /schematics`, `GET /schematics/:id` and `GET /versions`; image URL; version resolution. |
| `pkg/image` | new | Streaming download, zstd decoding, progress, sync, partition re-read, retry. |
| `talos2disk/` | new | `main.go` wiring the fall-through and the pipeline, `Dockerfile`, `README.md`. |
| `taloscmdline/`, `talosmeta/` | moved | Thin mains over `pkg/`, unchanged environment contracts, `Dockerfile`, `README.md`. |
| `docs/superpowers/specs/` | | This document plus the two existing action specs with paths corrected. |

Dependencies beyond those the two existing actions already use: `github.com/siderolabs/go-blockdevice/v2`, `github.com/ryanuber/go-glob`, `github.com/dustin/go-humanize`, `github.com/klauspost/compress` (zstd). No CEL engine and no Talos machinery module: the structured selector and the configuration subset are matched and parsed directly.

Logging is `slog` text to stdout, as in the existing actions.

## Failure handling

Before the write, every failure exits non-zero with the disk untouched: missing or unparsable `HARDWARE`, unparsable `userData`, unparsable selector, no matching disk, Factory rejection, unresolvable version. `DRY_RUN` stops at the same point on success.

After the write, a partition-table, UKI or META failure exits non-zero and leaves a written but unfinished disk. Re-running the action is safe: every step rewrites its output in full. Unmount failures are logged, not fatal, as in taloscmdline.

## Testing

Unit tests per package, run with `go test -race ./...`:

- `pkg/talosconfig`: multi-document configs, the v1alpha1 document among others, absent and malformed `userData`, `DISK_ID` placeholder, installer reference and tag parsing.
- `pkg/disks`: selector parsing and matching tables including Talos' documented examples (`>= 1TB`, `Samsung*`, `type: nvme`), the `name` rejection, implicit filters, path matching through symlinks, ordering, and enumeration against a fake sysfs tree with a fake prober.
- `pkg/detect`: cpuinfo and PCI fixtures for Intel, AMD, NVIDIA and arm64; out-of-band fallback cases.
- `pkg/factory`: httptest server for `POST /schematics` (id and canonical body, error status), `GET /schematics/:id`, `GET /versions`; version resolution tables; schematic determinism.
- `pkg/image`: httptest-served zstd blob written to a temp file, redirect following, retry on a failing first attempt.
- `pkg/partition`: node naming, wait, and the sysfs major:minor fallback against a fake tree.
- `pkg/talosnet`: the hostname, resolver and time server overrides on top of the existing mapping tests.
- Moved packages keep their existing tests.
- `talos2disk`: fall-through tables for every setting, and a pipeline test with fake prober, fake mounter and an httptest Factory against a diskfs-built GPT image holding `EFI` and `META` partitions; asserts the plan, the written image bytes, the META tag and the merged command lines. A `DRY_RUN` case asserts the disk is untouched.

Lint with `make lint`; build each image with `make <action>`.

Manual validation before switching the Template: `DRY_RUN` on a real machine through a one-off Workflow, then the loop-device and QEMU/OVMF recipe used for taloscmdline against the resulting disk image. The `HARDWARE` Template expression is checked against tink's renderer with a Hardware that has userData and out-of-band attributes.

## Out of scope and follow-ups

- **Upgrade-path drift.** The schematic the action registers includes microcode and GPU extensions that the runtime-extensions resolver cannot detect, so `machine.install.image` and the resolver's `talos.tinkerbell.org/installer-image` annotation name a smaller schematic. An upgrade through that installer drops the detected extensions. The action logs the difference at install time; closing the gap (reading `ImageFactorySchematic` from the node, or teaching the resolver the same rules from `status.attributes.outOfBand`) is tracked in runtime-extensions.
- **Extensions requested on the TinkerbellMachine** are not on the Hardware object; they must be set on the Hardware annotation or in `EXTENSIONS`.
- **The taloscmdline and talosmeta copies in the tinkerbell-community/actions fork** become redundant once this repository publishes images; removing them is a separate cleanup.
- **talosmeta's `MIRROR_HOST` path** still depends on tootles exposing interfaces on `/metadata`, as its README states. Unchanged here.
- Secure Boot images, NVIDIA extension selection, image checksum verification, and OCI image sources are not handled.
