# The Go toolchain runs in a pinned container, so nothing needs to be installed
# on the host. Caches live in-tree (gitignored) and are written as the current
# user so bind-mounted files are not left root-owned.
# Pinned to an exact Go patch + registry digest so host builds match the Dockerfile
# builders (go.mod requires go 1.26). Bump this and deploy/Dockerfile.* together.
GO_IMAGE ?= golang:1.26.4-trixie@sha256:792443b89f65105abba56b9bd5e97f680a80074ac62fc844a584212f8c8102c3
# The generator imports the backend module's shared data libs via a replace directive
# (see go.mod). Both repos are checked out side by side, so the container mounts the
# sibling backend repo too; the replace path ../censurado-web-backend resolves to it.
BACKEND_DIR ?= $(shell cd $(CURDIR)/../censurado-web-backend 2>/dev/null && pwd)
DOCKER_GO = docker run --rm -u $(shell id -u):$(shell id -g) -e HOME=/tmp -e GOCACHE=/work/censurado-web/.gocache -e GOMODCACHE=/work/censurado-web/.gomodcache -v $(CURDIR):/work/censurado-web -v $(BACKEND_DIR):/work/censurado-web-backend -w /work/censurado-web $(GO_IMAGE)

.PHONY: build test vet fmt tidy ci
build: ; $(DOCKER_GO) go build ./...
test:  ; $(DOCKER_GO) go test ./...
vet:   ; $(DOCKER_GO) go vet ./...
fmt:   ; $(DOCKER_GO) gofmt -l -w .
tidy:  ; $(DOCKER_GO) go mod tidy
ci: vet test
