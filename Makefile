.PHONY: verify test tidy doctor agent pin-check demo-docker

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
	go run ./cmd/agent -rules configs/example.rules.yaml -listen 127.0.0.1:5353 -pins dry-run

# Two CoreDNS hello-world upstreams + agent; exits non-zero if steering fails.
demo-docker:
	docker compose -f docker/demo/compose.yaml up --build --abort-on-container-exit --exit-code-from verify
	docker compose -f docker/demo/compose.yaml down --remove-orphans
