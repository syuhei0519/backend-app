# 本番用イメージを隔離して起動確認するため、CI 専用の最小テストイメージを作ります。
FROM golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS probe
WORKDIR /probe
COPY probe.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /smoke probe.go
FROM input
COPY --from=probe --chown=10001:10001 /smoke /oci-smoke
USER 10001:10001
RUN ["/oci-smoke"]
