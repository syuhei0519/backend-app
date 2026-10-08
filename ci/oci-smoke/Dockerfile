FROM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS probe
WORKDIR /probe
COPY probe.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /smoke probe.go
FROM input
COPY --from=probe --chown=10001:10001 /smoke /oci-smoke
RUN ["/oci-smoke"]
