# Kunai build and release.

VERSION   ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64
HOST      ?= user@your-hub
LDFLAGS   := -s -w -X 'github.com/hegade/kunai/internal/server.buildVersion=$(VERSION)'

.PHONY: build web bin release deploy test clean

## build: web app + local binary
build: web bin

web:
	cd web && npm install --no-fund --no-audit && npm run build

bin:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o kunai ./cmd/kunai

## release: cross-compile every platform into dist/ (embeds the current web build)
release: web
	rm -rf dist && mkdir -p dist
	for p in $(PLATFORMS); do \
		GOOS=$${p%-*} GOARCH=$${p#*-} CGO_ENABLED=0 \
		go build -ldflags="$(LDFLAGS)" -o dist/kunai-$$p ./cmd/kunai || exit 1; \
	done
	cp install.sh dist/
	ls -la dist/

## deploy: push a fresh linux build to $(HOST) and restart the service
deploy:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o /tmp/kunai-linux-amd64 ./cmd/kunai
	scp /tmp/kunai-linux-amd64 $(HOST):~/kunai.new
	ssh $(HOST) 'export XDG_RUNTIME_DIR=/run/user/$$(id -u); T=$$HOME/.local/bin/kunai; [ -f "$$T" ] || T=$$HOME/kunai; chmod +x ~/kunai.new && mv ~/kunai.new "$$T" && systemctl --user restart kunai && systemctl --user is-active kunai'

test:
	go test ./...

clean:
	rm -rf dist kunai
