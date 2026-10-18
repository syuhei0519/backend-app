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
