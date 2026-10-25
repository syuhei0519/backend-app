# tools/oci-input の見方

イメージの由来と OCI の archive・manifest・layer を検証する共有 module。

## 構成と入口

layout.go の検証処理を cmd 配下の各コマンドから呼びます。API 本体とは別の Go module です。

この階層のディレクトリと役割は次のとおりです。末端のファイルは親の説明から追えます。

| 場所 | 役割 |
| --- | --- |
| [cmd/](cmd) | CI から使う OCI 検査と起動試験用の変換コマンド。 |
