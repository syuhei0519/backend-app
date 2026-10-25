# 共有 CI ツール

`oci-input` は GitHub の source identity と archive / manifest / layer を照合する。
`release-record` は同じ image の SBOM と scan inventory を照合する。
`scan-gate` は各 repo の security policy を適用する。旧 GitLab API / registry client は削除した。

共通 Action は `.github/actions/shared-tools/action.yml`、配置処理は `ci/materialize-tools.sh`。
Action 自身の固定 commit にあるコードをコピーするので、別の SHA を処理内へ埋め込まない。
利用側は Action の固定参照と必要な module 名だけを宣言する。

ローカルでは対象 commit を checkout したこの repo から、空の配置先へ実行できる。

```sh
bash ci/materialize-tools.sh /path/to/consumer scan-gate
bash ci/test-shared-tools.sh
for tool in oci-input release-record scan-gate; do (cd tools/$tool && go test ./...); done
```

新しい Action を使う変更は、提供元の変更を先に通常の merge で取り込んでから利用側を取り込む。

## 構成と入口

この階層のディレクトリと役割は次のとおりです。末端のファイルは親の説明から追えます。

| 場所 | 役割 |
| --- | --- |
| [oci-input/](oci-input/README.md) | イメージの由来と OCI の archive・manifest・layer を検証する共有 module。 |
| [release-record/](release-record/README.md) | SBOM とイメージの構成情報を照合する共有 module。 |
| [scan-gate/](scan-gate/README.md) | 各 repo の方針に従って検出結果や描画 YAML を合否判定する共有 module。 |
