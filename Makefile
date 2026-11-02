# CodX Makefile

# Tool binaries
CONTROLLER_GEN ?= $(shell go env GOPATH)/bin/controller-gen

# Paths
API_DIR    := api/profile/v1
CRD_OUT    := charts/codx/crds
GEN_DEEPCOPY := $(API_DIR)/zz_generated.deepcopy.go

# Default target
.PHONY: all
all: build

.PHONY: generate
generate: deepcopy crd

.PHONY: deepcopy
deepcopy:
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt paths="./$(API_DIR)/..."

.PHONY: crd
crd:
	mkdir -p $(CRD_OUT)
	$(CONTROLLER_GEN) crd:crdVersions=v1 paths="./$(API_DIR)/..." output:crd:dir=$(CRD_OUT)

.PHONY: build
build:
	go build ./...

.PHONY: test
test:
	go test ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: docs-crd
docs-crd:
	python3 hack/gen_crd_docs.py

.PHONY: check-values-docs
check-values-docs:
	python3 hack/check_values_docs.py charts/codx/values.yaml

# Validate the syntax of the mermaid diagrams in the Markdown files with
# the real Mermaid parser (syntax only, no rendering). The node_modules are
# installed into a throwaway directory so the repository stays free of
# JavaScript dependencies. Requires node and npm on PATH.
# ":=" expands mktemp once: a lazy "?=" would run it per reference and the
# install and the NODE_PATH would point at two different directories.
MERMAID_CHECK_DIR := $(shell mktemp -d)

.PHONY: check-mermaid
check-mermaid:
	npm --prefix $(MERMAID_CHECK_DIR) install --no-audit --no-fund --silent mermaid@11 jsdom >/dev/null
	NODE_PATH=$(MERMAID_CHECK_DIR)/node_modules node hack/check_mermaid.mjs

# Scan the Go modules (go.mod/go.sum) for known CVEs, like the CI does on
# the Docker image. Requires trivy on PATH (https://trivy.dev).
# Fails on HIGH/CRITICAL findings with a fix available.
.PHONY: scan-cves
scan-cves:
	trivy filesystem --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 .
