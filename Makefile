.PHONY: build test vet plugin-check check

build:
	go build -trimpath -o bin/radiko-archive ./cmd/radiko-archive

test:
	go test ./...

vet:
	go vet ./...

plugin-check:
	@output="$$(yt-dlp -v 2>&1 || true)"; echo "$$output" | grep -q 'Extractor Plugins:.*RadikoTimeFreeIE'

check: test vet build
