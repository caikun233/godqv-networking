.PHONY: all build test clean server client gui

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

all: test build

test:
	go test ./pkg/... -v -race

build: server client

server:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server ./cmd/server/

client:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-client ./cmd/client/

gui:
	go build -ldflags="$(LDFLAGS)" -o dist/godqv-gui ./cmd/gui/

clean:
	rm -rf dist/

# Cross-compilation targets
# Note: GUI targets require CGO_ENABLED=1 and a C cross-compiler for the target platform.
# For CI builds, use the GitHub Actions workflow which uses platform-specific runners.
build-all: build-linux build-windows build-darwin

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-linux-amd64 ./cmd/server/
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-client-linux-amd64 ./cmd/client/
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o dist/godqv-gui-linux-amd64 ./cmd/gui/
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-linux-arm64 ./cmd/server/
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-client-linux-arm64 ./cmd/client/

build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-windows-amd64.exe ./cmd/server/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-client-windows-amd64.exe ./cmd/client/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o dist/godqv-gui-windows-amd64.exe ./cmd/gui/

build-darwin:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-darwin-amd64 ./cmd/server/
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-client-darwin-amd64 ./cmd/client/
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o dist/godqv-gui-darwin-amd64 ./cmd/gui/
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-server-darwin-arm64 ./cmd/server/
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/godqv-client-darwin-arm64 ./cmd/client/
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o dist/godqv-gui-darwin-arm64 ./cmd/gui/

genconfig:
	dist/godqv-server -genconfig
	dist/godqv-client -genconfig
