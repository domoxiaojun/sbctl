.PHONY: test check build-linux
test:
	go test ./...
check:
	go vet ./...
	go test -race ./...
build-linux:
	mkdir -p dist/linux-amd64 dist/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/linux-amd64/sbctl ./cmd/sbctl
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o dist/linux-arm64/sbctl ./cmd/sbctl
