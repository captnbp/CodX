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
