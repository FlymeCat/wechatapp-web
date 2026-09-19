# ============================================================================
# wechatapp-web Makefile
# ============================================================================
# Common tasks: build, run, test, swagger, docker.
#
# Configurable variables (override on the command line or in your env):
#   make GOMODCACHE=/path GOCACHE=/path build   # custom Go caches
#   make GOPROXY=... GOSUMDB=on build           # module proxy / checksum db
# ============================================================================

APP_NAME   := wechatapp-server
BIN_DIR    := bin
CMD_DIR    := ./cmd/server

# Go caches. Default to project-local directories so the build works even when
# the system-wide Go cache is read-only (CI / sandboxed environments).
GOMODCACHE ?= $(CURDIR)/.gomodcache
GOCACHE    ?= $(CURDIR)/.gocache

# Module proxy and checksum database (override as needed).
GOPROXY ?= https://goproxy.cn,direct
GOSUMDB ?= off

# Environment prefix applied to every go invocation.
GOENV := GOMODCACHE=$(GOMODCACHE) GOCACHE=$(GOCACHE) GOPROXY=$(GOPROXY) GOSUMDB=$(GOSUMDB)

# Swagger tool.
SWAG         := .tools/swag
SWAG_VERSION := v1.16.3
# Stamp file: only created after swag is installed. Changing SWAG_VERSION
# produces a new stamp name, which triggers a fresh install.
SWAG_STAMP   := .tools/.swag-installed-$(SWAG_VERSION)

# Docker image name/tag.
IMAGE_NAME := wechatapp-web
IMAGE_TAG  := latest

.PHONY: all build run test vet fmt tidy swagger clean \
        docker-build docker-run docker-stop help

all: build

## build: compile the server binary into ./bin
build:
	@mkdir -p $(BIN_DIR)
	$(GOENV) go build -o $(BIN_DIR)/$(APP_NAME) $(CMD_DIR)
	@echo "built: $(BIN_DIR)/$(APP_NAME)"

## run: start the server locally (requires REMOVE_BG_API_KEY)
run:
	$(GOENV) go run $(CMD_DIR)

## test: run all unit tests
test:
	$(GOENV) go test ./...

## vet: run go vet
vet:
	$(GOENV) go vet ./...

## fmt: format all Go sources
fmt:
	gofmt -w .

## tidy: sync go.mod / go.sum
tidy:
	$(GOENV) go mod tidy

## swagger: (re)generate API docs in ./docs from annotations
swagger: $(SWAG_STAMP)
	$(SWAG) init -g $(CMD_DIR)/main.go -d . -o docs

# Install the swag CLI only once (tracked by the stamp file). Running make
# swagger repeatedly no longer re-downloads/recompiles swag every time.
$(SWAG_STAMP):
	@mkdir -p $(CURDIR)/.tools
	GOBIN=$(CURDIR)/.tools $(GOENV) go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)
	@touch $@

## docker-build: build the Docker image
docker-build:
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .

## docker-run: run the Docker image (loads .env if present, maps port 8080)
docker-run: docker-build
	docker run --rm -d --name $(IMAGE_NAME) -p 8080:8080 \
		$(if $(wildcard .env),--env-file .env,) $(IMAGE_NAME):$(IMAGE_TAG)

## docker-stop: stop the running container
docker-stop:
	docker stop $(IMAGE_NAME) || true

## clean: remove build artifacts and local tooling
clean:
	rm -rf $(BIN_DIR) .tools
	@echo "cleaned $(BIN_DIR)/ and .tools/"

## help: show this help message
help:
	@echo "wechatapp-web — available targets:"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'
