# syntax=docker/dockerfile:1

# -- Build stage --
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code.
COPY . .

# Build the CodX binary as a static binary.
# CGO is disabled for a fully static binary compatible with distroless.
ARG TARGETOS=linux
ARG TARGETARCH=arm64
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN go build \
    -ldflags="-s -w -X main.version=$(git rev-parse --short HEAD 2>/dev/null || echo dev)" \
    -trimpath \
    -o /out/codx \
    ./cmd/codx

# -- Runtime stage --
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /

# Copy the binary from the builder.
COPY --from=builder /out/codx /codx

# The /tls directory is where cert-manager secrets are mounted.
# The /etc/codx directory is where the ConfigMap is mounted.
# These are created at runtime by Kubernetes volume mounts, but we
# ensure the nonroot user can read them.
USER nonroot:nonroot

EXPOSE 8443

ENTRYPOINT ["/codx"]
CMD ["-config", "/etc/codx/config.yaml"]
