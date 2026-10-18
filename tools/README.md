# 共有 CI ツール

`oci-input`、`release-record`、`scan-gate` の Go module はこの repo が正本。
frontend-app / application-manifest / platform-gitops は `ci/shared-tools.sh` の固定 commit を取得する。
Go の依存版と sum、CI の policy、安全拒否は保持する。consumer の policy は各 repo の `security/` に置く。

backend の image mode、SBOM epoch/release 正規化、runtime export と inspection、
manifest の host namespace 拒否・release reader・inspection consumer を一つの実装にまとめた。
GitLab API/registry の関数と fixtures は過去の明示的な互換試験用であり、GitHub URL に機械変換しない。
無効な `.gitlab-ci.yml` は削除し、現在の入口は `.github/workflows/` とする。

共有元 PR の commit を公開してから consumer CI を開始し、共有元を先にマージする。
consumer の commit pin は共有元の commit を保持する merge 方式を使うか、マージ後の固定 SHA へ更新する。
consumer の参照更新時は source identity、render 拒否、SBOM と repo 固有テストを実行する。

```sh
for tool in oci-input release-record scan-gate; do (cd tools/$tool && go test ./...); done
```
