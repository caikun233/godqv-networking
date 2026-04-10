.PHONY: all build test clean server gui

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

all: test build

test:
	go test ./pkg/... -v -race

build: server

server:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server ./cmd/server/

gui:
	go build -ldflags="$(LDFLAGS)" -o dist/godqv-gui.exe ./cmd/gui/

clean:
	rm -rf dist/

# Cross-compilation targets
# Note: GUI target requires CGO_ENABLED=1 and a C cross-compiler for the target platform.
# For CI builds, use the GitHub Actions workflow which uses platform-specific runners.
build-all: build-linux build-windows build-darwin

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-linux-amd64 ./cmd/server/
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-linux-arm64 ./cmd/server/

build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-windows-amd64.exe ./cmd/server/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o dist/godqv-gui-windows-amd64.exe ./cmd/gui/


build-darwin:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-darwin-amd64 ./cmd/server/
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-darwin-arm64 ./cmd/server/

