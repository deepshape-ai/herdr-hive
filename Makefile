.PHONY: build build-hive build-bee test test-installer vet package package-hive package-bee clean
VERSION ?= 0.1.0-dev
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
build: build-hive build-bee
build-hive:
	mkdir -p dist
	cd hive && GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o ../dist/hive ./cmd/hive
build-bee:
	mkdir -p dist
	cd bee && GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o ../dist/bee ./cmd/bee
test-installer:
	sh -n install.sh
	python3 -B tests/test_install.py
test: test-installer
	cd internal/update && go test -race ./...
	cd hive && go test -race ./...
	cd bee && go test -race ./...
vet:
	cd internal/update && go vet ./...
	cd hive && go vet ./...
	cd bee && go vet ./...
package: package-hive package-bee
package-hive: build-hive
	mkdir -p dist/packages/hive-$(GOOS)-$(GOARCH)
	cp dist/hive hive/README.md LICENSE dist/packages/hive-$(GOOS)-$(GOARCH)/
	cd dist/packages && COPYFILE_DISABLE=1 tar -czf ../hive-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz hive-$(GOOS)-$(GOARCH)
package-bee: build-bee
	mkdir -p dist/packages/bee-$(GOOS)-$(GOARCH)
	cp dist/bee bee/README.md LICENSE dist/packages/bee-$(GOOS)-$(GOARCH)/
	sed 's/^version = ".*"/version = "$(VERSION)"/' bee/plugin/herdr-plugin.toml > dist/packages/bee-$(GOOS)-$(GOARCH)/herdr-plugin.toml
	cd dist/packages && COPYFILE_DISABLE=1 tar -czf ../bee-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz bee-$(GOOS)-$(GOARCH)
clean:
	rm -rf dist
