# Actions

Tinkerbell Actions for installing [Talos Linux](https://www.talos.dev) on bare metal.
They compose into Tinkerbell Workflows the same way the upstream
[tinkerbell/actions](https://github.com/tinkerbell/actions) do.

| Name | Description |
| --- | --- |
| [talos2disk](/talos2disk/)       | Install Talos from the Image Factory: select the disk, register the schematic, write the image, stamp kernel arguments, write network configuration to META |
| [taloscmdline](/taloscmdline/)   | Set kernel arguments in Talos Linux unified kernel images |
| [talosmeta](/talosmeta/)         | Write Talos Linux network configuration to the META partition |

## Releases

Every push to `main` builds all actions for `linux/amd64` and `linux/arm64` and pushes them to
`ghcr.io/sidero-community/actions/<action>` tagged with the Git revision and `latest`. Pin the
revision tag in Workflow Templates; `latest` moves with every merge.

## Development

```
make test        # go test -race ./...
make lint        # golangci-lint, yamllint, hadolint, shellcheck
make talos2disk  # build one image for the host architecture
make images      # build every image
```

Design documents live in [docs/superpowers/specs](docs/superpowers/specs/).
