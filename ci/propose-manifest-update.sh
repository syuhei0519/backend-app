#!/bin/sh
# Goの提案helperを起動するCI入口。ci/manifest-update.goが環境の検証済みrelease tupleをMRへ記載する。
# このscript単独ではmerge・Argo Sync・Pod置換を実行しない。
set -eu
exec go run ci/manifest-update.go
