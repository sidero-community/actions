# Use bash
SHELL       := bash
.SHELLFLAGS := -o pipefail -euc
.ONESHELL:

# Second expansion is used by the image targets to depend on their respective binaries. It is
# necessary because automatic variables are not set on first expansion.
# See https://www.gnu.org/software/make/manual/html_node/Secondary-Expansion.html.
.SECONDEXPANSION:

# Define the list of actions that can be built.
ACTIONS := talos2disk taloscmdline talosmeta

# Platform for locally built images; defaults to the host architecture.
BUILD_PLATFORM ?= linux/$(shell go env GOARCH)

# Define the commit for tagging images.
GIT_COMMIT := $(shell git rev-parse HEAD)

# Define container registry details.
CONTAINER_REPOSITORY ?= ghcr.io/sidero-community/actions

include Rules.mk

.PHONY: help
help: ## Print this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[%\/0-9A-Za-z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
	@echo
	@echo Individual actions can be built with their name. For example, \`make talos2disk\`.

.PHONY: $(ACTIONS)
$(ACTIONS): ## Build a specific action image.
	docker buildx build --platform $(BUILD_PLATFORM) --load -t  $@:latest -f ./$@/Dockerfile .

.PHONY: images
images: ## Build all action images.
images: $(ACTIONS)

.PHONY: test
test: ## Run the unit tests.
	go test -race ./...

.PHONY: push
push: ## Push all action images.
push: $(addprefix push-,$(ACTIONS))

.PHONY: push-%
push-%: ## Push a specific action image to the registry. This recipe assumes you are already authenticated with the registry.
	IMAGE_NAME=$(CONTAINER_REPOSITORY)/$*
	docker tag $*:latest $$IMAGE_NAME:$(GIT_COMMIT)
	docker tag $*:latest $$IMAGE_NAME:latest
	docker push $$IMAGE_NAME:$(GIT_COMMIT)
	docker push $$IMAGE_NAME:latest

formatters: ## Run all formatters.
formatters: $(toolBins)
	git ls-files '*.go' | xargs -I% sh -c 'sed -i "/^import (/,/^)/ { /^\s*$$/ d }" % && bin/gofumpt -w %'
	git ls-files '*.go' | xargs -I% bin/goimports -w %

prepare-release:
	docker buildx create --name sidero-multiarch --use --driver docker-container || true

clean-release:
	docker buildx rm sidero-multiarch || true

.PHONY: release
release: ## Push all action images.
release: $(addprefix release-,$(ACTIONS))

.PHONY: release-%
release-%: ## Release an action for linux/amd64 and linux/arm64.
	IMAGE_NAME=$(CONTAINER_REPOSITORY)/$*
	docker buildx build --platform linux/amd64,linux/arm64 --push -t $$IMAGE_NAME:$(GIT_COMMIT) -t $$IMAGE_NAME:latest -f ./$*/Dockerfile .

include Lint.mk
