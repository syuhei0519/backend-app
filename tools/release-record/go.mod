// 公開証跡・SBOM の検証ツールを独立してビルドするための module と依存定義です。
module github.com/syuhei0519/backend-app/tools/release-record

go 1.27.0

require github.com/santhosh-tekuri/jsonschema/v6 v6.0.2

require golang.org/x/text v0.41.0 // indirect
