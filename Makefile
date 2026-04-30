.PHONY: all build generate build-all install uninstall uninstall-all clean help \
	test lint hooks fmt deps update-deps sqlc-check flatc-check sqlc-vet \
	fantasy-check fantasy-diff fantasy-sync fantasy-patch test-integration check run \
	devcontainer-up devcontainer-build devcontainer-generate devcontainer-verify \
	eval-build eval eval-fixtures eval-view eval-proof-full eval-clean eval-compare eval-test

# Build variables
BINARY_NAME=dragonscale
BUILD_DIR=bin
CMD_DIR=cmd/$(BINARY_NAME)
MAIN_GO=$(CMD_DIR)/main.go
OPSCTL_SRCS := $(shell find cmd/opsctl internal/opsctl -type f -name '*.go')

# Version
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short=8 HEAD 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date +%FT%T%z)
GO_VERSION=$(shell $(GO) version | awk '{print $$3}')
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.gitCommit=$(GIT_COMMIT) -X main.buildTime=$(BUILD_TIME) -X main.goVersion=$(GO_VERSION) -s -w"

# Go variables
GO?=go
GOFLAGS?=-v -trimpath -tags=stdjson
CGO_ENABLED?=1
MAKEFILE_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
DEVCONTAINER_WORKSPACE ?= $(MAKEFILE_DIR)

# Go command execution can optionally run through the devcontainer to match build env.
HAS_NPX := $(shell command -v npx >/dev/null 2>&1; echo $$?)
DEVCONTAINER_EXEC ?= 
IN_DOCKER := $(strip $(shell [ -f /.dockerenv ] && echo 1))
DEVCONTAINER_CLI := npx --yes @devcontainers/cli
DEVCONTAINER_UP_CMD := $(DEVCONTAINER_CLI) up --workspace-folder "$(DEVCONTAINER_WORKSPACE)"
DEVCONTAINER_EXEC_CMD := $(DEVCONTAINER_CLI) exec --workspace-folder "$(DEVCONTAINER_WORKSPACE)"
CGO_ENV=CGO_ENABLED=$(CGO_ENABLED)
ifeq ($(strip $(DEVCONTAINER_EXEC)),)
ifneq ($(strip $(IN_DOCKER)),1)
ifneq ($(strip $(shell test -d "$(DEVCONTAINER_WORKSPACE)/.devcontainer" && echo ok)),)
	DEVCONTAINER_EXEC := $(DEVCONTAINER_UP_CMD) && $(DEVCONTAINER_EXEC_CMD)
else ifeq ($(HAS_NPX),0)
	DEVCONTAINER_EXEC := $(DEVCONTAINER_UP_CMD) && $(DEVCONTAINER_EXEC_CMD)
endif
endif
endif
ifeq ($(strip $(DEVCONTAINER_EXEC)),)
	DEVCONTAINER_EXEC :=
endif
EVAL_BASH := $(if $(DEVCONTAINER_EXEC),$(DEVCONTAINER_EXEC) -- bash -lc,bash -lc)
PIPELINE_GO := $(if $(DEVCONTAINER_EXEC),$(DEVCONTAINER_EXEC) -- bash -lc 'git config --global --add safe.directory "$$PWD" >/dev/null 2>&1 || true; $(CGO_ENV) $(GO) "$$0" "$$@"',$(CGO_ENV) $(GO))
EVAL_GO := $(PIPELINE_GO)
EVAL_NPM := npx --yes

# Installation
INSTALL_PREFIX?=$(HOME)/.local
INSTALL_BIN_DIR=$(INSTALL_PREFIX)/bin
INSTALL_MAN_DIR=$(INSTALL_PREFIX)/share/man/man1

# Workspace and Skills
DRAGONSCALE_HOME?=$(HOME)/.dragonscale
WORKSPACE_DIR?=$(DRAGONSCALE_HOME)/workspace
WORKSPACE_SKILLS_DIR=$(WORKSPACE_DIR)/skills
BUILTIN_SKILLS_DIR=$(CURDIR)/skills

# OS detection
UNAME_S:=$(shell uname -s)
UNAME_M:=$(shell uname -m)

# Platform-specific settings
ifeq ($(UNAME_S),Linux)
	PLATFORM=linux
	ifeq ($(UNAME_M),x86_64)
		ARCH=amd64
	else ifeq ($(UNAME_M),aarch64)
		ARCH=arm64
	else ifeq ($(UNAME_M),loongarch64)
		ARCH=loong64
	else ifeq ($(UNAME_M),riscv64)
		ARCH=riscv64
	else
		ARCH=$(UNAME_M)
	endif
else
	$(error This project is Linux/CGO-only. Build requires a Linux host with glibc-compatible tooling.)
endif

BINARY_PATH=$(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)-$(ARCH)

# Compatibility shim: run everything through the Go wrapper.
OPSCTL_BIN = bin/opsctl
OPSCTL ?= $(OPSCTL_BIN)
OPSCTL_EVAL_ARGS ?= --no-color --format raw
OPSCTL_EVAL ?= $(EVAL_GO) run ./cmd/opsctl
DRAGONSCALE_PROMPTFOO_ARGS ?= --no-cache --no-progress-bar

$(OPSCTL_BIN): $(OPSCTL_SRCS)
	@mkdir -p $(dir $(OPSCTL_BIN))
	@go build -o $(OPSCTL_BIN) ./cmd/opsctl

# Default target
all: build

## generate: Run generate
generate: $(OPSCTL_BIN)
	@$(OPSCTL) generate

## build: Build the dragonscale binary for current platform
build: $(OPSCTL_BIN)
	@$(OPSCTL) build

## build-all: Build dragonscale for the current Linux/CGO target
build-all: $(OPSCTL_BIN)
	@$(OPSCTL) build-all

## install: Install dragonscale to system and copy builtin skills
install: $(OPSCTL_BIN)
	@$(OPSCTL) install

## uninstall: Remove dragonscale from system
uninstall: $(OPSCTL_BIN)
	@$(OPSCTL) uninstall

## uninstall-all: Remove dragonscale and all data
uninstall-all: $(OPSCTL_BIN)
	@$(OPSCTL) uninstall-all

## clean: Remove build artifacts
clean: $(OPSCTL_BIN)
	@$(OPSCTL) clean

## vet: Run go vet for static analysis
vet: $(OPSCTL_BIN)
	@$(OPSCTL) vet

## test: Run tests
test: $(OPSCTL_BIN)
	@$(OPSCTL) test

## fmt: Format Go code
fmt: $(OPSCTL_BIN)
	@$(OPSCTL) fmt

## lint: Run all linting checks (format + vet + build)
lint: $(OPSCTL_BIN)
	@$(OPSCTL) lint

## hooks: Install git pre-commit and commit-msg hooks
hooks: $(OPSCTL_BIN)
	@$(OPSCTL) hooks

## deps: Download dependencies
deps: $(OPSCTL_BIN)
	@$(OPSCTL) deps

## update-deps: Update dependencies
update-deps: $(OPSCTL_BIN)
	@$(OPSCTL) update-deps

## sqlc-check: Verify sqlc generation is idempotent for current tree
sqlc-check: $(OPSCTL_BIN)
	@$(OPSCTL) sqlc-check

## flatc-check: Verify FlatBuffers generation is idempotent for current tree
flatc-check: $(OPSCTL_BIN)
	@$(OPSCTL) flatc-check

## sqlc-vet: Run sqlc vet rules (no-unbounded-delete, one-select-requires-limit-1)
sqlc-vet: $(OPSCTL_BIN)
	@$(OPSCTL) sqlc-vet

# ---------------------------------------------------------------------------
# Fantasy SDK vendor management
# ---------------------------------------------------------------------------
FANTASY_VERSION ?=

## fantasy-check: Compare vendored Fantasy SDK against latest upstream
fantasy-check: $(OPSCTL_BIN)
	@$(OPSCTL) fantasy-check

## fantasy-diff: Show diff between vendored and upstream (optional FANTASY_VERSION=vX.Y.Z)
fantasy-diff: $(OPSCTL_BIN)
	@$(OPSCTL) fantasy-diff

## fantasy-sync: Full sync of vendored Fantasy SDK (optional FANTASY_VERSION=vX.Y.Z)
fantasy-sync: $(OPSCTL_BIN)
	@$(OPSCTL) fantasy-sync

## fantasy-patch: Save local modifications as a patch (requires NAME=description)
fantasy-patch: $(OPSCTL_BIN)
	@$(OPSCTL) fantasy-patch

## test-integration: Run integration tests (requires build tags)
test-integration: $(OPSCTL_BIN)
	@$(OPSCTL) test-integration

## check: Run vet, fmt, sqlc vet, and verify dependencies
check: $(OPSCTL_BIN)
	@$(OPSCTL) check

## run: Build and run dragonscale
run: $(OPSCTL_BIN)
	@$(OPSCTL) run $(ARGS)

## devcontainer-build: Build the local devcontainer image via npx devcontainer CLI
devcontainer-build: $(OPSCTL_BIN)
	@$(OPSCTL) devcontainer-build

## devcontainer-up: Start/update the local devcontainer via npx devcontainer CLI
devcontainer-up: $(OPSCTL_BIN)
	@$(OPSCTL) devcontainer-up

## devcontainer-generate: Run go/sqlc/flatc generation inside devcontainer
devcontainer-generate: $(OPSCTL_BIN)
	@$(OPSCTL) devcontainer-generate

## devcontainer-verify: Validate generated code inside devcontainer
devcontainer-verify: $(OPSCTL_BIN)
	@$(OPSCTL) devcontainer-verify

# ---------------------------------------------------------------------------
# Evaluation Harness
# ---------------------------------------------------------------------------

## eval-build: Build the eval runner from the current branch
eval-build:
	@DRAGONSCALE_PROMPTFOO_ARGS="$(DRAGONSCALE_PROMPTFOO_ARGS)" $(OPSCTL_EVAL) $(OPSCTL_EVAL_ARGS) eval-build

## eval: Run the eval suite against the current build
eval:
	@DRAGONSCALE_PROMPTFOO_ARGS="$(DRAGONSCALE_PROMPTFOO_ARGS)" $(OPSCTL_EVAL) $(OPSCTL_EVAL_ARGS) eval

## eval-fixtures: Reset workspace to a known state and seed fixture files for eval
eval-fixtures:
	@DRAGONSCALE_PROMPTFOO_ARGS="$(DRAGONSCALE_PROMPTFOO_ARGS)" $(OPSCTL_EVAL) $(OPSCTL_EVAL_ARGS) eval-fixtures

## eval-view: Open the promptfoo results viewer
eval-view:
	@DRAGONSCALE_PROMPTFOO_ARGS="$(DRAGONSCALE_PROMPTFOO_ARGS)" $(OPSCTL_EVAL) $(OPSCTL_EVAL_ARGS) eval-view

## eval-proof-full: Run local provider-backed full eval proof with threshold and artifacts
eval-proof-full:
	@PROMPTFOO_PASS_RATE_THRESHOLD="$${PROMPTFOO_PASS_RATE_THRESHOLD:-100}" SKIP_DEVCONTAINER_WRAPPER=1 DEVCONTAINER_EXEC= DRAGONSCALE_PROMPTFOO_ARGS="$(DRAGONSCALE_PROMPTFOO_ARGS)" $(CGO_ENV) $(GO) run ./cmd/opsctl $(OPSCTL_EVAL_ARGS) eval-proof-full

eval-clean:
	@DRAGONSCALE_PROMPTFOO_ARGS="$(DRAGONSCALE_PROMPTFOO_ARGS)" $(OPSCTL_EVAL) $(OPSCTL_EVAL_ARGS) eval-clean

## eval-compare: A/B comparison of current branch vs main
eval-compare:
	@DRAGONSCALE_PROMPTFOO_ARGS="$(DRAGONSCALE_PROMPTFOO_ARGS)" $(OPSCTL_EVAL) $(OPSCTL_EVAL_ARGS) eval-compare

## eval-test: Run Go-native component evals
eval-test:
	@DRAGONSCALE_PROMPTFOO_ARGS="$(DRAGONSCALE_PROMPTFOO_ARGS)" $(OPSCTL_EVAL) $(OPSCTL_EVAL_ARGS) eval-test

## help: Show this help message
help: $(OPSCTL_BIN)
	@$(OPSCTL) help
