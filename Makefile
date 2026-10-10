VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.Version=$(VERSION)

.PHONY: test build dist
test:
	go vet ./...
	go test ./...

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o windy-node ./cmd/windy-node

dist:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/windy-node-linux-amd64 ./cmd/windy-node
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/windy-node-linux-arm64 ./cmd/windy-node
	cd dist && sha256sum windy-node-* > SHA256SUMS 2>/dev/null || shasum -a 256 windy-node-* > SHA256SUMS
