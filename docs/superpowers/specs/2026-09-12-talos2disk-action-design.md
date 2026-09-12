# talos2disk action design

Date: 2026-09-12. Status: approved design, awaiting implementation plan.

Repositories touched: `sidero-community/actions` (this repository, new), the
`tinkerbell-community/tinkerbell` fork (tootles `/metadata`), and the
`cluster-api-runtime-extensions-tinkerbell` chart (the install Workflow
Template). The `tinkerbell-community/actions` fork is not changed here.

## Goal

Replace the four-action Talos install sequence (image2disk, taloscmdline,
talosmeta, reboot) with two actions: `talos2disk` followed by the existing
waitdaemon reboot. `talos2disk` runs inside the OSIE on the target machine and

1. fetches the machine's Hardware data from the Tinkerbell metadata service,
2. selects the install disk with a Talos-shaped disk selector,
3. determines the Talos Image Factory schematic from what it can see on the
   machine plus the Hardware annotations, and registers it with the Factory,
4. resolves the Talos version,
5. streams the Factory's raw metal image onto the disk,
6. stamps deploy-level kernel arguments into the UKI command lines,
7. writes the Talos network configuration to the META partition,
8. exits. Rebooting stays a separate action.

The repository also hosts `taloscmdline` and `talosmeta` as standalone actions
with their existing contracts, built from the same shared packages under
`pkg/`. It carries the build, lint and release scaffolding of
`tinkerbell/actions` so the three vendor-specific actions are built and
published the same way upstream actions are.

## Decisions taken during design

| Question | Decision |
| --- | --- |
| Who determines the schematic | The action, from the machine and the Hardware annotations. It registers the schematic with the Factory itself. The runtime-extensions resolver keeps running but the Template no longer consumes its `operating_system.slug`. |
| Talos version source | `TALOS_VERSION` env override, else `metadata.instance.operating_system.version`, else the `talos.tinkerbell.org/contract` annotation. Full versions are used exactly; a bare minor resolves to the newest non-broken patch via the Factory. |
| Disk selector | `DISK_SELECTOR` in the exact shape of Talos `machine.install.diskSelector`, default `{ "size": ">= 100GB" }`. Multiple matches pick the first by device name, as Talos does. |
| Detection rules | NVMe present, CPU vendor, display-class PCI vendor, architecture. |
| Hardware data | Fetched in-action from tootles `/metadata` by source IP, the rootio pattern. Nothing large is passed through the environment. |
| Kernel arguments | Stamped into the UKI after the write. The schematic carries no `extraKernelArgs`, so it stays environment-agnostic. |
| Structure | New repository, three actions, shared code under `pkg/`. |

## talos2disk contract

### Environment

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `MIRROR_HOST` | yes, unless `HARDWARE_SPEC` is set | | Host of the Tinkerbell metadata service. The action requests `http://MIRROR_HOST:PORT/metadata`; tootles identifies the machine by the request's source IP, so the action must run with host networking. |
| `METADATA_SERVICE_PORT` | no | `7080` | Port of the consolidated Tinkerbell HTTP server. |
| `HARDWARE_SPEC` | no | | Hardware data as JSON in the `/metadata` shape. Overrides the metadata service. Intended for bench testing and `DRY_RUN` without tootles. |
| `TALOS_VERSION` | no | | Full version (`v1.14.2`) or bare minor (`v1.14`). Overrides the version carried by the Hardware data. |
| `DISK_SELECTOR` | no | `{ "size": ">= 100GB" }` | Talos `machine.install.diskSelector` document, YAML or JSON. |
| `KERNEL_ARGS` | no | empty | Whitespace-separated arguments merged into every `EFI/Linux/Talos-*.efi` command line. Deploy-level values belong here: `talos.config=…`, `net.ifnames=0`, consoles. Empty skips the step. |
| `EXTENSIONS` | no | | Comma-separated official extensions merged into the schematic. |
| `OVERLAY` | no | | `name@image`. Overrides the `talos.tinkerbell.org/overlay` annotation. |
| `FACTORY_URL` | no | `https://factory.talos.dev` | Image Factory base URL. |
| `NETWORK_CONFIG` | no | | Complete Talos network configuration document written verbatim to META key `0xa`, bypassing the Hardware mapping. |
| `LINK_NAMING` | no | inferred | `kernel` or `predictable`. When unset, `kernel` if the final command line contains `net.ifnames=0`, else `predictable`. |
| `STRIP_SIGNATURE` | no | `false` | Allow rewriting an Authenticode-signed UKI; the signature is removed. |
| `RETRY_DURATION_MINUTES` | no | `10` | Window for retrying the image download with exponential backoff. `0` disables retries. |
| `DRY_RUN` | no | `false` | Resolve everything, print the plan, exit 0 without touching the disk. |

### Hardware data consumed

From the `/metadata` document (see the tootles change below):

| Field | Used for |
| --- | --- |
| `interfaces[].dhcp.*` | Network configuration: links, addresses, routes, resolvers, time servers, VLANs. |
| `metadata.instance.hostname`, `metadata.instance.ips[]` | Hostname and external IPs in the network configuration. |
| `metadata.instance.operating_system.version` | Talos version when `TALOS_VERSION` is unset. |
| `annotations["talos.tinkerbell.org/system-extensions"]` | Per-machine extensions, comma separated. |
| `annotations["talos.tinkerbell.org/overlay"]` | Image Factory overlay, `name@image`. |
| `annotations["talos.tinkerbell.org/contract"]` | Bare-minor version fallback. |
| `disks[].device` | Not used for selection. Logged as a warning when the selected disk is not listed. |

`userData` and `vendorData` are never requested or logged.

### Pipeline

Every remote call and every decision happens before the first byte is written.

1. Parse the environment. An unparsable `DISK_SELECTOR` or `NETWORK_CONFIG` fails immediately.
2. Fetch the Hardware data from `HARDWARE_SPEC` or the metadata service (60 s timeout). A 404 is reported as "no Hardware matched the source IP".
3. Enumerate whole disks and evaluate the selector. Log every eligible disk with its properties, the candidates, and the chosen disk. More than one candidate logs a warning naming them all.
4. Detect machine facts and build the schematic. `POST /schematics`; log the canonical body the Factory returns and its ID.
5. Resolve the version. Bare minors call `GET /versions`.
6. Build the image URL `<FACTORY_URL>/image/<id>/<version>/metal-<arch>.raw.zst` and log the full plan. `DRY_RUN` exits here.
7. Stream the image onto the disk: HTTP GET following redirects, zstd decompression, progress every 5 s, `fsync`, `BLKRRPART`. Retry from the beginning within the retry window.
8. Re-read the GPT and locate the `EFI` and `META` partitions by label. Fail if either is missing.
9. If `KERNEL_ARGS` is set, obtain the EFI partition device node, mount it as vfat, and merge the arguments into every matching UKI.
10. Build the network document from `NETWORK_CONFIG` or the Hardware data and write META key `0xa`. When neither yields a document (no interface has a static address), log a warning and skip.
11. Log a summary: disk, schematic ID and extensions, version, image URL, resulting command lines, network document. Exit 0.

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

| Signal | Source | Effect |
| --- | --- | --- |
| Architecture | the action's own `GOARCH` | `metal-<arch>` image path; `bootloader: sd-boot` on arm64. |
| NVMe | any enumerated disk with transport `nvme` | `siderolabs/nvme-cli` |
| CPU vendor | `vendor_id` in `/proc/cpuinfo` | `GenuineIntel` → `siderolabs/intel-ucode`; `AuthenticAMD` → `siderolabs/amd-ucode`. arm64 has no `vendor_id` and gets no microcode extension. |
| Display-class PCI device | `/sys/bus/pci/devices/*/class` starting `0x03`, with `vendor` | `0x1002` → `siderolabs/amdgpu`; `0x8086` → `siderolabs/i915`. NVIDIA (`0x10de`) is logged and not added, since its extension variants are a deployment choice. |
| Explicit | `talos.tinkerbell.org/system-extensions` annotation and `EXTENSIONS` | merged |
| Overlay | `OVERLAY`, else the `talos.tinkerbell.org/overlay` annotation | `overlay.name` and `overlay.image` |

Extensions are deduplicated and sorted so identical machines yield identical schematics. The schematic carries no `extraKernelArgs`, `meta` or embedded configuration. The Factory ignores kernel arguments for the installer image anyway, and `KERNEL_ARGS` covers the raw image.

Registration is `POST /schematics` with `Content-Type: application/yaml`. The response `{id, schematic}` is authoritative; the canonical body is logged. The ID is the SHA-256 of the canonical body, so re-registration is idempotent. The Factory validates extension names; an unknown name fails the action before any write.

## Version resolution

Order: `TALOS_VERSION`, then `metadata.instance.operating_system.version`, then the `talos.tinkerbell.org/contract` annotation. No value at all is an error.

A full version `vX.Y.Z` or `vX.Y.Z-<pre>` is used exactly. A bare minor `vX.Y` resolves through `GET /versions` (which excludes versions the Factory marks broken): keep entries with prefix `vX.Y.`, drop prereleases (any `-`), pick the highest patch. No candidate is an error.

## Image write

The download follows redirects because the Factory may answer `302` to object storage. The body is decoded as a zstd stream and written to the whole-disk device; progress (bytes read and written) is logged every 5 s. On completion the device is synced and `BLKRRPART` is issued. Failures within `RETRY_DURATION_MINUTES` retry with exponential backoff (1 s doubling, capped at 30 s), rewriting from offset 0. zstd frame checksums detect corruption in transit; Factory `.sha256` sidecars are an enterprise feature and are not used.

## Partition access after the write

`pkg/partition` locates a partition by GPT label and derives its device node with the kernel naming rules (`/dev/sda1`, `/dev/nvme0n1p1`, `-part1` for `/dev/disk/by-*`). Because the write and the mount happen in one process, the node may not exist yet: the package waits up to 10 s for the host to create it, and otherwise reads `/sys/block/<disk>/<part>/dev` and creates a node with `mknod` under a private directory. META is written through the whole-disk device at the partition offset and needs no node.

## UKI command lines and META

The UKI step is the existing taloscmdline behaviour behind `pkg/cmdline`: mount the EFI partition as vfat, glob `EFI/Linux/Talos-*.efi`, merge `KERNEL_ARGS` into every `.cmdline` section (key=value replaces, bare flags append once), rewrite each UKI to a temporary file and rename it over the original. A signed UKI is refused unless `STRIP_SIGNATURE` is set.

The META step is the existing talosmeta behaviour: `NETWORK_CONFIG` is validated and written verbatim, otherwise `interfaces[]` and `metadata.instance` map onto the Talos metal platform network configuration on the `platform` layer, and the document is stored under key `0xa` with every other key preserved. Interface names resolve through `iface_name`, else the sysfs lookup by MAC with kernel or predictable naming. The naming mode is inferred from the final command line unless `LINK_NAMING` overrides it.

## Why `/metadata` and not the EC2 endpoints

tootles has two frontends, checked in the fork on 2026-09-12: the EC2-style
`/2009-04-04/meta-data/*` tree (instance-id, hostname, local-ipv4, public-ipv4,
tags, `operating-system/{slug,distro,version,image_tag}`) plus `/user-data`,
and the rootio-era `/metadata` document. Neither serves `interfaces[]` or the
Hardware annotations today, so the HackInstance extension below is required in
any case. Once it lands, one `/metadata` request carries everything talos2disk
needs, and the EC2 endpoints are not consulted. All tootles endpoints remain
available to the action should a later need arise; none of them requires a
further tootles change.

## tootles change in the tinkerbell fork

`tootles/internal/frontend/hack` serves `GET /metadata` for the Hardware whose interface carries the request's source IP. `toHackInstance` marshals `Hardware.spec` to JSON and unmarshals it into `pkg/data.HackInstance`, which today declares only `metadata.instance.storage`. The change:

- `HackInstance` gains `interfaces`, `disks`, and `metadata.instance` `id`, `hostname`, `ips` and `operating_system`, with JSON tags equal to the Hardware CRD so the marshal round-trip keeps working.
- `HackInstance` gains a top-level `annotations` map populated from the Hardware `ObjectMeta` in `toHackInstance`.
- `userData` and `vendorData` stay out of `/metadata`; the EC2 `/user-data` endpoint already serves user data.
- `TestGetHackInstance` grows cases for interfaces, instance metadata and annotations.

This also makes talosmeta's `MIRROR_HOST` path work end to end; its README section on metadata service requirements is updated accordingly.

## Workflow Template in the runtime-extensions chart

`files/talos-install-template.yaml` becomes two actions. The reboot action is unchanged.

```yaml
actions:
  - name: "Install Talos"
    image: __ACTIONS_REPOSITORY__/talos2disk:__ACTIONS_TAG__
    timeout: 9600
    namespaces:
      network: host
    environment:
      MIRROR_HOST: __TINKERBELL_IP__
      KERNEL_ARGS: "console=tty0 console=ttyAMA0,115200 net.ifnames=0 talos.config=__TOOTLES_USER_DATA_URL__"
  - name: "reboot"
    # unchanged waitdaemon action
```

Host networking is required so tootles sees the machine's own IP. `__TINKERBELL_IP__` is substituted by the chart from the same values that build the tootles URL, and `template.actions.repository` defaults to `ghcr.io/sidero-community/actions`. The Template body no longer references `operating_system`, so the content-addressed name changes and a new Template object ships, per the chart's existing rule. The Workflow-CREATE gate stays; it now guarantees that `operating_system.version` is present when the machine fetches `/metadata`.

## Repository layout and scaffolding

Module `github.com/sidero-community/actions`, Go 1.26.3 (required by go-adv). License MIT. The taloscmdline and talosmeta sources move from the `tinkerbell-community/actions` fork with import paths rewritten; they are this repository's author's own work. The image streaming code is written fresh rather than copied from Apache-2.0 image2disk.

Scaffolding adapted from `tinkerbell/actions`: `Makefile` (`ACTIONS := talos2disk taloscmdline talosmeta`, `BUILD_PLATFORM` from the host, `CONTAINER_REPOSITORY ?= ghcr.io/sidero-community/actions`, per-action `docker buildx` targets, `release-%` multi-arch push), `Rules.mk`, `Lint.mk`, `.golangci.yml`, `.yamllint`, `.gitignore`, `.github/workflows/ci.yml` (test, lint, build matrix over the three actions) and `release.yml` (build and push amd64 and arm64 to ghcr.io tagged with the Git SHA and `latest`), `README.md` with the action table, a short `CONTRIBUTING.md`. Each action has a `golang:1.26-alpine` to `scratch` Dockerfile that copies CA certificates, since the Factory and the metadata service are reached over HTTP(S).

| Path | Origin | Contents |
| --- | --- | --- |
| `pkg/hardware` | moved from talosmeta | Hardware data types in the `/metadata` shape, now including `annotations`, `disks` and `metadata.instance.operating_system`; `Fetch` against the metadata service. |
| `pkg/talosnet` | moved | Network document mapping and the sysfs link namer. |
| `pkg/meta` | moved | META partition location and tag read/write via go-adv. |
| `pkg/uki`, `pkg/kargs` | moved | UKI `.cmdline` rewrite with the `ukitest` builder; argument merging. |
| `pkg/partition` | moved from efipart, extended | Locate by GPT label, device node naming, node wait and `mknod` fallback. |
| `pkg/cmdline` | extracted from taloscmdline main | Mount, glob, merge and rewrite behind a `Mounter` interface. |
| `pkg/disks` | new | Enumeration behind a `Prober` interface with the go-blockdevice implementation; the selector type, parsing and matching; selection. |
| `pkg/detect` | new | Architecture, CPU vendor, PCI display vendors, NVMe presence; returns extension names. |
| `pkg/factory` | new | Schematic types in the Factory's field order; client for `POST /schematics` and `GET /versions`; image URL; version resolution. |
| `pkg/image` | new | Streaming download, zstd decoding, progress, sync, partition re-read, retry. |
| `talos2disk/` | new | `main.go` wiring the pipeline, `Dockerfile`, `README.md`. |
| `taloscmdline/`, `talosmeta/` | moved | Thin mains over `pkg/`, unchanged environment contracts, `Dockerfile`, `README.md`. |
| `docs/superpowers/specs/` | | This document plus the two existing action specs with paths corrected. |

Dependencies beyond those the two existing actions already use: `github.com/siderolabs/go-blockdevice/v2`, `github.com/ryanuber/go-glob`, `github.com/dustin/go-humanize`, `github.com/klauspost/compress` (zstd). No CEL engine: the structured selector is matched directly.

Logging is `slog` text to stdout, as in the existing actions.

## Failure handling

Before the write, every failure exits non-zero with the disk untouched: missing `MIRROR_HOST`, metadata fetch errors or 404, unparsable selector or Hardware data, no matching disk, Factory rejection, unresolvable version. `DRY_RUN` stops at the same point on success.

After the write, a partition-table, UKI or META failure exits non-zero and leaves a written but unfinished disk. Re-running the action is safe: every step rewrites its output in full. Unmount failures are logged, not fatal, as in taloscmdline.

## Testing

Unit tests per package, run with `go test -race ./...`:

- `pkg/disks`: selector parsing and matching tables including Talos' documented examples (`>= 1TB`, `Samsung*`, `type: nvme`), the `name` rejection, implicit filters, ordering, and enumeration against a fake sysfs tree with a fake prober.
- `pkg/detect`: cpuinfo and PCI fixtures for Intel, AMD, NVIDIA and arm64.
- `pkg/factory`: httptest server for `POST /schematics` (id and canonical body, error status), `GET /versions`; version resolution tables; schematic determinism.
- `pkg/image`: httptest-served zstd blob written to a temp file, redirect following, retry on a failing first attempt.
- `pkg/partition`: node naming, wait, and the sysfs major:minor fallback against a fake tree.
- Moved packages keep their existing tests.
- `talos2disk`: a pipeline test with fake prober, fake mounter, httptest Factory and metadata service, against a diskfs-built GPT image holding `EFI` and `META` partitions; asserts the plan, the written image bytes, the META tag and the merged command lines. A `DRY_RUN` case asserts the disk is untouched.

Lint with `make lint`; build each image with `make <action>`.

Manual validation before switching the Template: `DRY_RUN` on a real machine through a one-off Workflow, then the loop-device and QEMU/OVMF recipe used for taloscmdline against the resulting disk image.

## Out of scope and follow-ups

- **Upgrade-path drift.** The runtime-extensions resolver still publishes `talos.tinkerbell.org/installer-image` from its own schematic, which cannot include the microcode and GPU extensions the action detects. The upgrade path should eventually read the schematic from the running node (`ImageFactorySchematic` resource). Tracked in runtime-extensions, not here.
- **Extensions requested on the TinkerbellMachine** are invisible to `/metadata`; they must be set on the Hardware annotation or in `EXTENSIONS`.
- **The taloscmdline and talosmeta copies in the tinkerbell-community/actions fork** become redundant once this repository publishes images; removing them is a separate cleanup.
- Secure Boot images, NVIDIA extension selection, image checksum verification, and OCI image sources are not handled.
