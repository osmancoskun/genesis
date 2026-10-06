.PHONY: verify test tidy doctor agent menu setup pin-check demo-docker demo-docker-split host-dry host-cleanup

verify:
	go run ./cmd/verify

test:
	go test ./...

tidy:
	go mod tidy

doctor:
	go run ./cmd/ctl doctor

pin-check:
	go run ./cmd/ctl pin check

agent:
	go run ./cmd/agent -config configs/local.yaml

menu:
	go run ./cmd/ctl menu

setup:
	go run ./cmd/ctl setup

# Two CoreDNS hello-world upstreams + agent; exits non-zero if steering fails.
demo-docker:
	docker compose -f docker/demo/compose.yaml up --build --abort-on-container-exit --exit-code-from verify
	docker compose -f docker/demo/compose.yaml down --remove-orphans

# LAN vs WARP-like split entirely in compose nets (never --network=host; host routing untouched).
demo-docker-split:
	mkdir -p docker/split/.build
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o docker/split/.build/agent ./cmd/agent
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o docker/split/.build/ifacebyip ./docker/split/ifacebyip
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o docker/split/.build/verifycheck ./docker/split/verifycheck
	docker compose -f docker/split/compose.yaml up --build --abort-on-container-exit --exit-code-from verify
	docker compose -f docker/split/compose.yaml down --remove-orphans

# Host try: Discord via CloudflareWARP, default via eno1 (dry-run by default).
host-dry:
	./scripts/host-try.sh

host-cleanup:
	./scripts/host-cleanup.sh
