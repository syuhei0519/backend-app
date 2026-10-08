# syntax=docker/dockerfile:1.7
# CIのBuildKitが読む複数段ビルド定義。ソースと固定依存から実行用イメージを作る。
# 入口はci/build-oci.sh、成果物のdigest検査はtools/oci-input/layout.go。
# VCS_REF/VCS_SOURCEをOCIラベルへ結び、build用依存を最終runtimeへそのまま持ち込まない。
FROM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VCS_REF=unknown
# trimpathとGo symbolを残す。trimpathでbuild-infoからldflagsが省略されても、Trivyがバイナリ内のstamp済みmain module versionを読めるようにする。
RUN go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-X main.buildCommit=${VCS_REF} -X main.version=0.0.0+git.${VCS_REF}" -o /out/server ./cmd/server
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG VCS_REF=unknown
ARG VCS_SOURCE=unknown
LABEL org.opencontainers.image.revision=$VCS_REF org.opencontainers.image.source=$VCS_SOURCE
COPY --from=build --chown=10001:10001 /out/server /server
COPY --chown=10001:10001 migrations /migrations
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/server"]
