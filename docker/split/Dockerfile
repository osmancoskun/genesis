# Runtime-only image; binaries built on the host (no apt/go-proxy inside build).
FROM alpine:3.20
COPY .build/agent /usr/local/bin/agent
COPY .build/ifacebyip /usr/local/bin/ifacebyip
EXPOSE 5353/udp
ENTRYPOINT ["/usr/local/bin/agent"]
