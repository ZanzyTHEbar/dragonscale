.PHONY: all build install uninstall clean help test lint hooks \
	fantasy-check fantasy-diff fantasy-sync fantasy-patch \
	flatc-check devcontainer-up devcontainer-build devcontainer-generate devcontainer-verify

# Build variables
BINARY_NAME=dragonscale
BUILD_DIR=bin
CMD_DIR=cmd/$(BINARY_NAME)
MAIN_GO=$(CMD_DIR)/main.go

# Version
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short=8 HEAD 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date +%FT%T%z)
GO_VERSION=$(shell $(PIPELINE_GO) version | awk '{print $$3}')
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.gitCommit=$(GIT_COMMIT) -X main.buildTime=$(BUILD_TIME) -X main.goVersion=$(GO_VERSION) -s -w"

# Go variables
GO?=go
GOFLAGS?=-v -trimpath -tags stdjson

# Go command execution can optionally run through the devcontainer to match build env.
HAS_NPX := $(shell command -v npx >/dev/null 2>&1; echo $$?)
DEVCONTAINER_EXEC ?= 
ifeq ($(strip $(DEVCONTAINER_EXEC)),)
ifeq ($(HAS_NPX),0)
	DEVCONTAINER_EXEC := npx --yes @devcontainers/cli exec --workspace-folder .
endif
endif
EVAL_BASH := $(if $(DEVCONTAINER_EXEC),$(DEVCONTAINER_EXEC) -- bash -lc,bash -lc)
PIPELINE_GO := $(if $(DEVCONTAINER_EXEC),$(DEVCONTAINER_EXEC) -- bash -lc 'git config --global --add safe.directory "$$PWD" >/dev/null 2>&1 || true; $(GO) "$$0" "$$@"',$(GO))
EVAL_GO := $(PIPELINE_GO)
EVAL_NPM := npx

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
else ifeq ($(UNAME_S),Darwin)
	PLATFORM=darwin
	ifeq ($(UNAME_M),x86_64)
		ARCH=amd64
	else ifeq ($(UNAME_M),arm64)
		ARCH=arm64
	else
		ARCH=$(UNAME_M)
	endif
else
	PLATFORM=$(UNAME_S)
	ARCH=$(UNAME_M)
endif

BINARY_PATH=$(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)-$(ARCH)

# Default target
all: build

## generate: Run generate
generate:
	@echo "Run generate..."
	@rm -r ./$(CMD_DIR)/workspace 2>/dev/null || true
	@$(PIPELINE_GO) generate ./...
	@echo "Run generate complete"

## build: Build the dragonscale binary for current platform
build: generate
	@echo "Building $(BINARY_NAME) for $(PLATFORM)/$(ARCH)..."
	@mkdir -p $(BUILD_DIR)
	@$(PIPELINE_GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_PATH) ./$(CMD_DIR)
	@echo "Build complete: $(BINARY_PATH)"
	@ln -sf $(BINARY_NAME)-$(PLATFORM)-$(ARCH) $(BUILD_DIR)/$(BINARY_NAME)

## build-all: Build dragonscale for all platforms
build-all: generate
	@echo "Building for multiple platforms..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(PIPELINE_GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)
	GOOS=linux GOARCH=arm64 $(PIPELINE_GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./$(CMD_DIR)
	GOOS=linux GOARCH=loong64 $(PIPELINE_GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-loong64 ./$(CMD_DIR)
	GOOS=linux GOARCH=riscv64 $(PIPELINE_GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-riscv64 ./$(CMD_DIR)
	GOOS=darwin GOARCH=arm64 $(PIPELINE_GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)
	GOOS=windows GOARCH=amd64 $(PIPELINE_GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./$(CMD_DIR)
	@echo "All builds complete"

## install: Install dragonscale to system and copy builtin skills
install: build
	@echo "Installing $(BINARY_NAME)..."
	@mkdir -p $(INSTALL_BIN_DIR)
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@chmod +x $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@echo "Installed binary to $(INSTALL_BIN_DIR)/$(BINARY_NAME)"
	@echo "Installation complete!"

## uninstall: Remove dragonscale from system
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@rm -f $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@echo "Removed binary from $(INSTALL_BIN_DIR)/$(BINARY_NAME)"
	@echo "Note: Only the executable file has been deleted."
	@echo "If you need to delete all configurations (config.json, workspace, etc.), run 'make uninstall-all'"

## uninstall-all: Remove dragonscale and all data
uninstall-all:
	@echo "Removing workspace and skills..."
	@rm -rf $(DRAGONSCALE_HOME)
	@echo "Removed workspace: $(DRAGONSCALE_HOME)"
	@echo "Complete uninstallation done!"

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

## vet: Run go vet for static analysis
vet:
	@$(PIPELINE_GO) vet ./...

## test: Run tests
test:
	@$(PIPELINE_GO) test ./...

## fmt: Format Go code
fmt:
	@$(PIPELINE_GO) fmt ./...

## lint: Run all linting checks (format + vet + build)
lint: fmt vet
	@$(PIPELINE_GO) build ./...
	@echo "Lint OK"

## hooks: Install git pre-commit and commit-msg hooks
hooks:
	@echo "Installing git hooks..."
	@ln -sf ../../scripts/hooks/pre-commit .git/hooks/pre-commit
	@ln -sf ../../scripts/hooks/commit-msg .git/hooks/commit-msg
	@chmod +x .git/hooks/pre-commit .git/hooks/commit-msg
	@echo "  ✓ pre-commit  → scripts/hooks/pre-commit"
	@echo "  ✓ commit-msg  → scripts/hooks/commit-msg"
	@echo "Hooks installed. Skip with: git commit --no-verify"

## deps: Download dependencies
deps:
	@$(PIPELINE_GO) mod download
	@$(PIPELINE_GO) mod verify

## update-deps: Update dependencies
update-deps:
	@$(PIPELINE_GO) get -u ./...
	@$(PIPELINE_GO) mod tidy

## sqlc-check: Verify sqlc generation is idempotent for current tree
sqlc-check:
	@echo "Checking sqlc generation..."
	@set -e; repo="$$(pwd)"; before="$$(mktemp)"; after="$$(mktemp)"; \
	snapshot() { \
		git -c safe.directory="$$repo" diff --binary -- pkg/memory/sqlc/; \
		git -c safe.directory="$$repo" ls-files --others --exclude-standard -- pkg/memory/sqlc/ | LC_ALL=C sort | while IFS= read -r f; do \
			[ -f "$$f" ] && sha256sum "$$f"; \
		done; \
	}; \
	snapshot > "$$before"; \
	sqlc generate -f pkg/memory/sqlc/sqlc.yaml; \
	snapshot > "$$after"; \
	if ! cmp -s "$$before" "$$after"; then \
		echo "::error::sqlc generated code is stale. Run 'sqlc generate -f pkg/memory/sqlc/sqlc.yaml' and commit."; \
		rm -f "$$before" "$$after"; \
		exit 1; \
	fi; \
	rm -f "$$before" "$$after"
	@echo "sqlc OK"

## flatc-check: Verify FlatBuffers generation is idempotent for current tree
flatc-check:
	@echo "Checking flatc generation..."
	@set -e; repo="$$(pwd)"; before="$$(mktemp)"; after="$$(mktemp)"; \
	snapshot() { \
		git -c safe.directory="$$repo" diff --binary -- pkg/itr/itrfb/ pkg/tools/mapopsfb/; \
		git -c safe.directory="$$repo" ls-files --others --exclude-standard -- pkg/itr/itrfb/ pkg/tools/mapopsfb/ | LC_ALL=C sort | while IFS= read -r f; do \
			[ -f "$$f" ] && sha256sum "$$f"; \
		done; \
	}; \
	snapshot > "$$before"; \
	$(PIPELINE_GO) generate ./pkg/itr ./pkg/tools; \
	snapshot > "$$after"; \
	if ! cmp -s "$$before" "$$after"; then \
		echo "::error::FlatBuffers generated code is stale. Run 'go generate ./pkg/itr ./pkg/tools' and commit."; \
		rm -f "$$before" "$$after"; \
		exit 1; \
	fi; \
	rm -f "$$before" "$$after"
	@echo "flatc OK"

## sqlc-vet: Run sqlc vet rules (no-unbounded-delete, one-select-requires-limit-1)
sqlc-vet:
	@echo "Running sqlc vet..."
	@sqlc vet -f pkg/memory/sqlc/sqlc.yaml
	@echo "sqlc vet OK"

# ---------------------------------------------------------------------------
# Fantasy SDK vendor management
# Usage: make fantasy-diff FANTASY_VERSION=v0.9.0
#        make fantasy-sync FANTASY_VERSION=v0.9.0
# ---------------------------------------------------------------------------
FANTASY_VERSION ?=

## fantasy-check: Compare vendored Fantasy SDK against latest upstream
fantasy-check:
	@./scripts/sync-fantasy.sh --check

## fantasy-diff: Show diff between vendored and upstream (optional FANTASY_VERSION=vX.Y.Z)
fantasy-diff:
	@./scripts/sync-fantasy.sh --diff $(FANTASY_VERSION)

## fantasy-sync: Full sync of vendored Fantasy SDK (optional FANTASY_VERSION=vX.Y.Z)
fantasy-sync:
	@./scripts/sync-fantasy.sh --sync $(FANTASY_VERSION)

## fantasy-patch: Save local modifications as a patch (requires NAME=description)
fantasy-patch:
	@./scripts/sync-fantasy.sh --save-patch $(NAME)

## test-integration: Run integration tests (requires build tags)
test-integration:
	@echo "Running integration tests..."
	@$(PIPELINE_GO) test -tags integration -count=1 -timeout 120s -v ./pkg/memory/...
	@echo "Integration tests OK"

## check: Run vet, fmt, sqlc vet, and verify dependencies
check: deps fmt vet sqlc-vet test

## run: Build and run dragonscale
run: build
	@$(BUILD_DIR)/$(BINARY_NAME) $(ARGS)

## devcontainer-build: Build the local devcontainer image via npx devcontainer CLI
devcontainer-build:
	@npx --yes @devcontainers/cli build --workspace-folder .

## devcontainer-up: Start/update the local devcontainer via npx devcontainer CLI
devcontainer-up:
	@npx --yes @devcontainers/cli up --workspace-folder .

## devcontainer-generate: Run go/sqlc/flatc generation inside devcontainer
devcontainer-generate:
	@npx --yes @devcontainers/cli exec --workspace-folder . -- bash -lc "go generate ./pkg/itr ./pkg/tools && sqlc generate -f pkg/memory/sqlc/sqlc.yaml"

## devcontainer-verify: Validate generated code inside devcontainer
devcontainer-verify:
	@npx --yes @devcontainers/cli exec --workspace-folder . -- make flatc-check sqlc-check

# ---------------------------------------------------------------------------
# Evaluation Harness
# Usage: make eval-build && make eval
#        make eval-compare       (A/B: branch vs main)
# ---------------------------------------------------------------------------

## eval-build: Build the eval runner from the current branch
eval-build:
	@echo "Building eval runner..."
	@$(EVAL_GO) generate ./...
	@mkdir -p eval/bin
	@$(EVAL_GO) build $(GOFLAGS) $(LDFLAGS) -o eval/bin/eval-runner ./eval/cmd/eval-runner
	@echo "Eval runner built: eval/bin/eval-runner"

## eval: Run the eval suite against the current build
eval: eval-build eval-fixtures
	@echo "Running eval suite..."
	@$(EVAL_BASH) 'cd eval && DRAGONSCALE_EVAL_CONFIG="./configs/default.json" $(EVAL_NPM) promptfoo eval --config promptfooconfig.yaml --no-cache --no-progress-bar'
	@echo "Results: eval/results/latest.json"
	@echo "View: cd eval && $(EVAL_NPM) promptfoo view"

## eval-fixtures: Reset workspace to a known state and seed fixture files for eval
## Uses XDG paths: ~/.local/share/dragonscale/sandbox/ and ~/.local/share/dragonscale/skills/
eval-fixtures:
	@$(EVAL_BASH) '\
	mkdir -p "$$HOME/.local/share/dragonscale/sandbox"; \
	rm -f "$$HOME/.local/share/dragonscale/sandbox/eval_test_output.txt" \
	      "$$HOME/.local/share/dragonscale/sandbox/test_steps.txt" \
	      "$$HOME/.local/share/dragonscale/sandbox/eval_checkpoint.txt" \
	      "$$HOME/.local/share/dragonscale/sandbox/chain_test.txt" \
	      "$$HOME/.local/share/dragonscale/sandbox/current_year.txt" \
	      "$$HOME/.local/share/dragonscale/sandbox/result.txt" \
	      "$$HOME/.local/share/dragonscale/sandbox/progressive_test.txt" \
	      "$$HOME/.local/share/dragonscale/sandbox/os_name.txt"; \
	rm -rf "$$HOME/.local/share/dragonscale/sandbox/project"; \
	printf '"'"'dragonscale eval fixture — hello from the eval harness\nThis is line two of the fixture file.\n'"'"' > "$$HOME/.local/share/dragonscale/sandbox/eval_fixture.txt"; \
	cp -f eval/fixtures/sample_data.txt "$$HOME/.local/share/dragonscale/sandbox/sample_data.txt"; \
	mkdir -p "$$HOME/.local/share/dragonscale/skills"; \
	cp -rf eval/fixtures/skills/* "$$HOME/.local/share/dragonscale/skills/" 2>/dev/null || true'

## eval-view: Open the promptfoo results viewer
eval-view:
	@$(EVAL_BASH) 'cd eval && $(EVAL_NPM) promptfoo view'

eval-clean:
	@rm -rf eval/results
	@rm -rf eval/bin

## eval-compare: A/B comparison of current branch vs main
eval-compare:
	@$(EVAL_BASH) 'cd eval && EVAL_NPM_CMD=$(EVAL_NPM) ./scripts/compare.sh --repeat 3'

## eval-test: Run Go-native component evals
eval-test:
	@echo "Running Go-native evals..."
	@$(EVAL_GO) test -v ./eval/go_evals/...

## help: Show this help message
help:
	@echo "dragonscale Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
	@echo ""
	@echo "Examples:"
	@echo "  make build              # Build for current platform"
	@echo "  make install            # Install to ~/.local/bin"
	@echo "  make uninstall          # Remove from /usr/local/bin"
	@echo "  make install-skills     # Install skills to workspace"
	@echo ""
	@echo "Environment Variables:"
	@echo "  DEVCONTAINER_EXEC        # Override to force host or custom container command (set empty to disable)"
	@echo "  INSTALL_PREFIX          # Installation prefix (default: ~/.local)"
	@echo "  WORKSPACE_DIR           # Workspace directory (default: ~/.dragonscale/workspace)"
	@echo "  VERSION                 # Version string (default: git describe)"
	@echo ""
	@echo "Current Configuration:"
	@echo "  Platform: $(PLATFORM)/$(ARCH)"
	@echo "  Binary: $(BINARY_PATH)"
	@echo "  Install Prefix: $(INSTALL_PREFIX)"
	@echo "  Workspace: $(WORKSPACE_DIR)"
