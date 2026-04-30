.PHONY: build run clean deps test build-riscv64 package-riscv64

# Binary name
BINARY=podmanview

# Version (from git tag or "dev")
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# Build flags
LDFLAGS=-s -w -X main.Version=$(VERSION)

# Build for current platform
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/podmanview

# Run the application
run: build
	./$(BINARY)

# Install dependencies
deps:
	go mod download
	go mod tidy

# Clean build artifacts
clean:
	rm -f $(BINARY)
	rm -f $(BINARY)-*
	rm -f *.tar.gz

# Build for ARM64 Linux
build-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-arm64 ./cmd/podmanview

# Package for ARM64
package-arm64: build-arm64
	tar -czvf $(BINARY)-$(VERSION)-linux-arm64.tar.gz \
		--transform 's,$(BINARY)-linux-arm64,$(BINARY),' \
		$(BINARY)-linux-arm64 web/

# Build for RISC-V 64-bit Linux
build-riscv64:
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-riscv64 ./cmd/podmanview

# Package for RISC-V 64-bit
package-riscv64: build-riscv64
	tar -czvf $(BINARY)-$(VERSION)-linux-riscv64.tar.gz \
		--transform 's,$(BINARY)-linux-riscv64,$(BINARY),' \
		$(BINARY)-linux-riscv64 web/

# Run tests
test:
	go test -v ./...

# Development mode with no auth
dev: build
	./$(BINARY) -no-auth

# Format code
fmt:
	go fmt ./...

# Lint code (requires golangci-lint)
lint:
	golangci-lint run

# Show help
help:
	@echo "Available targets:"
	@echo "  build         - Build for current platform"
	@echo "  run           - Build and run"
	@echo "  dev           - Build and run with -no-auth"
	@echo "  deps          - Download dependencies"
	@echo "  clean         - Remove build artifacts"
	@echo "  build-arm64   - Build for ARM64 Linux"
	@echo "  package-arm64 - Build and package for ARM64"
	@echo "  build-riscv64 - Build for RISC-V 64-bit Linux"
	@echo "  package-riscv64 - Build and package for RISC-V"
	@echo "  test          - Run tests"
	@echo "  fmt           - Format code"
	@echo "  lint          - Lint code"
