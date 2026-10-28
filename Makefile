BINARY := bin/music
GO_BUILD_FLAGS := -buildvcs=false -trimpath
VERSION ?= devel
PREFIX ?= $(HOME)/.local

.PHONY: all build install uninstall clean fmt fmt-check test vet vuln

all: test build

build:
	mkdir -p bin
	CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) .

install: build
	install -Dm755 $(BINARY) "$(DESTDIR)$(PREFIX)/bin/music"

uninstall:
	rm -f "$(DESTDIR)$(PREFIX)/bin/music"

clean:
	rm -f $(BINARY) coverage.out

fmt:
	gofmt -w *.go

fmt-check:
	test -z "$$(gofmt -l *.go)"

test:
	go test -buildvcs=false -race -count=1 ./...

vet:
	go vet -buildvcs=false ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 -mode=source -scan=symbol ./...
