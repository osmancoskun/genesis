# genesis agent image (DNS stub + pin hooks)
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

FROM debian:bookworm-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends iproute2 \
	&& rm -rf /var/lib/apt/lists/*
COPY --from=build /out/agent /usr/local/bin/agent
COPY configs/docker-demo.rules.yaml /etc/genesis/rules.yaml
EXPOSE 5353/udp
ENTRYPOINT ["/usr/local/bin/agent"]
CMD ["-rules", "/etc/genesis/rules.yaml", "-listen", "0.0.0.0:5353", "-pins", "noop"]
