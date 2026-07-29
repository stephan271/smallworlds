# The in-cluster Operator Console image.
#
# The same binary is the Bootstrap Launcher on an Operator's computer and the
# console in a pod; the difference is the --serve-console flag this image sets.
# That is deliberate: one build, one embedded client, one set of tests — the
# console cannot drift from the launcher it grew out of.
#
# Unlike the Hermes and Remediation agents, which run their Python straight from
# a ConfigMap in a stock image, a Go binary with an embedded client needs a real
# build. It is kept to three stages and no runtime package manager.

FROM node:22-alpine AS web
WORKDIR /src
# The lockfile is copied first so a source-only change does not reinstall the
# whole toolchain.
COPY web/package.json web/package-lock.json ./web/
RUN cd web && npm ci
COPY api ./api
COPY web ./web
# The Svelte build writes into internal/webui/dist, which the Go build embeds.
RUN mkdir -p internal/webui/dist \
    && cd web \
    && npm run generate:api \
    && npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY api ./api
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
ARG VERSION=devel
# CGO is off: every dependency is pure Go (the SQLite driver included), which is
# what lets the runtime stage carry no libc and no shell.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X github.com/stephan271/smallworlds/operator-console/internal/buildinfo.Version=${VERSION}" \
      -o /out/smallworlds-admin ./cmd/smallworlds-admin

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/smallworlds-admin /usr/local/bin/smallworlds-admin
# The console reads the cluster and writes only its own Activity Record; it needs
# no root, no shell, and no writable filesystem.
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/smallworlds-admin", "--serve-console"]
