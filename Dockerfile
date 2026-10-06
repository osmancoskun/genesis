# path-director agent image (DNS stub + pin hooks)
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

FROM debian:bookworm-slim
COPY --from=build /out/agent /usr/local/bin/agent
COPY configs/docker-demo.rules.yaml /etc/path-director/rules.yaml
EXPOSE 5353/udp
ENTRYPOINT ["/usr/local/bin/agent"]
CMD ["-rules", "/etc/path-director/rules.yaml", "-listen", "0.0.0.0:5353", "-pins", "noop"]
