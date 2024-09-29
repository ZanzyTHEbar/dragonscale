ifneq (,$(wildcard ./.env))
    include .env
    export
endif

# Binary name
# TODO: implement git versioning into the name
BINARY=decision-engine

# Build the application
all: build

build:
	@echo "Building..."
	@go build -o ./bin/$(BINARY) cmd/api/main.go

# Run the application
run:
	@echo "Running..."
	@go run cmd/api/main.go

# Test the application
test:
	@echo "Testing..."
	@go test ./tests -v

# Clean the binary
clean:
	@echo "Cleaning..."
	@go clean -testcache
	@rm -rf ./bin
	@rm -rf tmp

# Live Reload
dev:
	@if command -v air > /dev/null; then \
	    air; \
	    echo "Watching...";\
	else \
	    read -p "Go's 'air' is not installed on your machine. Do you want to install it? [Y/n] " choice; \
	    if [ "$$choice" != "n" ] && [ "$$choice" != "N" ]; then \
	        go install github.com/air-verse/air@latest; \
	        air; \
	        echo "Watching...";\
	    else \
	        echo "You chose not to install air. Exiting..."; \
	        exit 1; \
	    fi; \
	fi

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all             Build the application"
	@echo "  build           Build the application"
	@echo "  run             Run the application"
	@echo "  test            Test the application"
	@echo "  clean           Clean the binary"
	@echo "  dev             Live reload"
	@echo "  help            Show this help message"

%:
	@:

.PHONY: all build run test clean dev help
