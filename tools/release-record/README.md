# SBOM 検証

GitHub の OCI 検査で、公式 schema・config digest・部品 inventory を照合する。
API・registry client と旧 GitLab release-record 処理は終了した。

## 構成と入口

この階層のディレクトリと役割は次のとおりです。末端のファイルは親の説明から追えます。

| 場所 | 役割 |
| --- | --- |
| [cmd/](cmd) | CI から SBOM 検証を呼ぶコマンド。 |
| [schemas/](schemas) | 固定した外部 SBOM schema とライセンス。直接編集しません。 |
