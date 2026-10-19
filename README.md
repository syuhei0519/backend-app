# backend-app

Go標準HTTPサーバーとpgxによるAccount API。`server migrate`でforward-only migrationを適用します。

Go・HTTP/DB処理・SQL migration・イメージ・CIは本repository、migration実行Job・account固有DB/PVC・接続Secret参照はapplication-manifest、Argo Application/AppProject/Namespaceはplatform-gitopsが所有します。DBのSecret更新だけではDB roleの認証情報は変わりません。rotationの実行は利用者承認が必要です。

固定版台帳の正本はplatform-gitopsの `bootstrap/versions.lock.yaml` です。`ci/tool-versions.env` はPE-001時点で未作成であり、現在の実行値は `.github/workflows/ci.yml` / Dockerfileにあります。後続PEで追加する際は台帳との対応を照合します。`frontend` / `backend` / `application-gitops` は別系統で、今回の実装対象ではありません。[PE-001基準記録](https://github.com/syuhei0519/platform-gitops/blob/main/docs/implementation/PE-001-baseline.md) を参照してください。

## Order API (B1)

Order runs independently through `cmd/order-server` with its own database, role,
embedded migrations and history. See [API and migration usage](docs/order-api.md)
and [OpenAPI](docs/order-openapi.yaml), including disposable DB test commands.
New Order creation checks Account existence through `ORDER_ACCOUNT_URL` with a
bounded HTTP call. Unknown Accounts are rejected; an unset URL returns 503 for
creation. This intentionally changes B1's acceptance of nonexistent references.
Stored references remain historical: Account deletion retains Orders, and no
ownership/authorization guarantee is provided. See [the #7 communication contract](docs/order-account-communication.md).
Independent Order image/release and Kubernetes deployment are subsequent work.
