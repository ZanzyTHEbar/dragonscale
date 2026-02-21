.PHONY: all build install uninstall clean help test lint hooks \
	fantasy-check fantasy-diff fantasy-sync fantasy-patch

# Build variables
BINARY_NAME=picoclaw
BUILD_DIR=bin
CMD_DIR=cmd/$(BINARY_NAME)
MAIN_GO=$(CMD_DIR)/main.go

# Version
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short=8 HEAD 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date +%FT%T%z)
GO_VERSION=$(shell $(GO) version | awk '{print $$3}')
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.gitCommit=$(GIT_COMMIT) -X main.buildTime=$(BUILD_TIME) -X main.goVersion=$(GO_VERSION) -s -w"

# Go variables
GO?=go
GOFLAGS?=-v -trimpath -tags stdjson

# Installation
INSTALL_PREFIX?=$(HOME)/.local
INSTALL_BIN_DIR=$(INSTALL_PREFIX)/bin
INSTALL_MAN_DIR=$(INSTALL_PREFIX)/share/man/man1

# Workspace and Skills
PICOCLAW_HOME?=$(HOME)/.picoclaw
WORKSPACE_DIR?=$(PICOCLAW_HOME)/workspace
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
	@$(GO) generate ./...
	@echo "Run generate complete"

## build: Build the picoclaw binary for current platform
build: generate
	@echo "Building $(BINARY_NAME) for $(PLATFORM)/$(ARCH)..."
	@mkdir -p $(BUILD_DIR)
	@$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_PATH) ./$(CMD_DIR)
	@echo "Build complete: $(BINARY_PATH)"
	@ln -sf $(BINARY_NAME)-$(PLATFORM)-$(ARCH) $(BUILD_DIR)/$(BINARY_NAME)

## build-all: Build picoclaw for all platforms
build-all: generate
	@echo "Building for multiple platforms..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)
	GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./$(CMD_DIR)
	GOOS=linux GOARCH=loong64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-loong64 ./$(CMD_DIR)
	GOOS=linux GOARCH=riscv64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-riscv64 ./$(CMD_DIR)
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./$(CMD_DIR)
	@echo "All builds complete"

## install: Install picoclaw to system and copy builtin skills
install: build
	@echo "Installing $(BINARY_NAME)..."
	@mkdir -p $(INSTALL_BIN_DIR)
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@chmod +x $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@echo "Installed binary to $(INSTALL_BIN_DIR)/$(BINARY_NAME)"
	@echo "Installation complete!"

## uninstall: Remove picoclaw from system
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@rm -f $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@echo "Removed binary from $(INSTALL_BIN_DIR)/$(BINARY_NAME)"
	@echo "Note: Only the executable file has been deleted."
	@echo "If you need to delete all configurations (config.json, workspace, etc.), run 'make uninstall-all'"

## uninstall-all: Remove picoclaw and all data
uninstall-all:
	@echo "Removing workspace and skills..."
	@rm -rf $(PICOCLAW_HOME)
	@echo "Removed workspace: $(PICOCLAW_HOME)"
	@echo "Complete uninstallation done!"

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

## vet: Run go vet for static analysis
vet:
	@$(GO) vet ./...

## test: Run tests
test:
	@$(GO) test ./...

## fmt: Format Go code
fmt:
	@$(GO) fmt ./...

## lint: Run all linting checks (format + vet + build)
lint: fmt vet
	@$(GO) build ./...
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
	@$(GO) mod download
	@$(GO) mod verify

## update-deps: Update dependencies
update-deps:
	@$(GO) get -u ./...
	@$(GO) mod tidy

## sqlc-check: Verify sqlc-generated code is up to date
sqlc-check:
	@echo "Checking sqlc generation..."
	@sqlc generate -f pkg/memory/sqlc/sqlc.yaml
	@git diff --exit-code -- pkg/memory/sqlc/ || (echo "::error::sqlc generated code is stale. Run 'sqlc generate -f pkg/memory/sqlc/sqlc.yaml' and commit." && exit 1)
	@echo "sqlc OK"

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
	@$(GO) test -tags integration -count=1 -timeout 120s -v ./pkg/memory/...
	@echo "Integration tests OK"

## check: Run vet, fmt, sqlc vet, and verify dependencies
check: deps fmt vet sqlc-vet test

## run: Build and run picoclaw
run: build
	@$(BUILD_DIR)/$(BINARY_NAME) $(ARGS)

# ---------------------------------------------------------------------------
# Evaluation Harness
# Usage: make eval-build && make eval
#        make eval-compare       (A/B: branch vs main)
# ---------------------------------------------------------------------------

## eval-build: Build the eval runner from the current branch
eval-build: generate
	@echo "Building eval runner..."
	@mkdir -p eval/bin
	@$(GO) build $(GOFLAGS) $(LDFLAGS) -o eval/bin/eval-runner ./eval/cmd/eval-runner
	@echo "Eval runner built: eval/bin/eval-runner"

## eval: Run the eval suite against the current build
eval: eval-build eval-fixtures
	@echo "Running eval suite..."
	@cd eval && PICOCLAW_EVAL_CONFIG="./configs/default.json" npx promptfoo eval --config promptfooconfig.yaml --no-cache --no-progress-bar
	@echo "Results: eval/results/latest.json"
	@echo "View: cd eval && npx promptfoo view"

## eval-fixtures: Reset workspace to a known state and seed fixture files for eval
## Uses XDG paths: ~/.local/share/picoclaw/sandbox/ and ~/.local/share/picoclaw/skills/
eval-fixtures:
	@mkdir -p $(HOME)/.local/share/picoclaw/sandbox
	@rm -f $(HOME)/.local/share/picoclaw/sandbox/eval_test_output.txt \
	       $(HOME)/.local/share/picoclaw/sandbox/test_steps.txt \
	       $(HOME)/.local/share/picoclaw/sandbox/eval_checkpoint.txt \
	       $(HOME)/.local/share/picoclaw/sandbox/chain_test.txt \
	       $(HOME)/.local/share/picoclaw/sandbox/current_year.txt \
	       $(HOME)/.local/share/picoclaw/sandbox/result.txt \
	       $(HOME)/.local/share/picoclaw/sandbox/progressive_test.txt \
	       $(HOME)/.local/share/picoclaw/sandbox/os_name.txt
	@rm -rf $(HOME)/.local/share/picoclaw/sandbox/project
	@printf 'picoclaw eval fixture — hello from the eval harness\nThis is line two of the fixture file.\n' > $(HOME)/.local/share/picoclaw/sandbox/eval_fixture.txt
	@cp -f eval/fixtures/sample_data.txt $(HOME)/.local/share/picoclaw/sandbox/sample_data.txt
	@mkdir -p $(HOME)/.local/share/picoclaw/skills
	@cp -rf eval/fixtures/skills/* $(HOME)/.local/share/picoclaw/skills/ 2>/dev/null || true

## eval-view: Open the promptfoo results viewer
eval-view:
	@cd eval && npx promptfoo view

eval-clean:
	@rm -rf eval/results
	@rm -rf eval/bin

## eval-compare: A/B comparison of current branch vs main
eval-compare:
	@./eval/scripts/compare.sh --repeat 3

## eval-test: Run Go-native component evals
eval-test:
	@echo "Running Go-native evals..."
	@$(GO) test -v ./eval/go_evals/...

## help: Show this help message
help:
	@echo "picoclaw Makefile"
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
	@echo "  INSTALL_PREFIX          # Installation prefix (default: ~/.local)"
	@echo "  WORKSPACE_DIR           # Workspace directory (default: ~/.picoclaw/workspace)"
	@echo "  VERSION                 # Version string (default: git describe)"
	@echo ""
	@echo "Current Configuration:"
	@echo "  Platform: $(PLATFORM)/$(ARCH)"
	@echo "  Binary: $(BINARY_PATH)"
	@echo "  Install Prefix: $(INSTALL_PREFIX)"
	@echo "  Workspace: $(WORKSPACE_DIR)"
