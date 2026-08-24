.PHONY: build test vet check

build:
	go build -trimpath -o bin/radiko-archive ./cmd/radiko-archive

test:
	go test ./...

vet:
	go vet ./...

check: test vet build
