# backend-app

## Existing kind deployment (PLAT-195/198)

For `kind-learning-20261018-69c763`, the ordinary deployment entry is a reviewed
Git commit/PR followed by Argo CD sync. Follow the [current deployment contract](https://github.com/syuhei0519/platform-gitops/blob/main/learning/existing-kind/DEPLOYMENT.md).
Manual workload apply, image mutation and rollout restart from historical learning
instructions are retired. Existing Argo Core is reused; the old platform-lab and
isolated Account trial instructions are not the current cluster's deployment route.
This entry-point change does not claim that the pending PLAT-177/199 acceptance has passed.


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

## 構成と入口

Account・Order・Payment・Notification の API と、各サービスの専用 DB への保存処理を管理します。

この階層のディレクトリと役割は次のとおりです。末端のファイルは親の説明から追えます。

| 場所 | 役割 |
| --- | --- |
| [.github/](.github/README.md) | GitHub の CI、Issue、PR の入口。実行条件は workflows に置きます。 |
| [ci/](ci/README.md) | 検査の実行手順と境界テスト。GitHub から呼ぶ処理は github にまとめます。 |
| [cmd/](cmd/README.md) | サービスごとの実行ファイルの入口。設定の読み込み、起動、終了を担当します。 |
| [docs/](docs/README.md) | 設計上の約束、検証結果、運用・引き継ぎの文書。過去の記録と現在の手順を区別して読みます。 |
| [internal/](internal/README.md) | API の業務処理、HTTP、設定、保存、計測を役割別に分けた内部コード。 |
| [migrations/](migrations) | Account の DB 初期化 SQL と、それを Go に埋め込む入口。 |
| [notification-migrations/](notification-migrations) | Notification 専用 DB の migration。既存の SQL は適用履歴として保持します。 |
| [order-migrations/](order-migrations) | Order 専用 DB の migration。既存の SQL は適用履歴として保持します。 |
| [payment-migrations/](payment-migrations) | Payment 専用 DB の migration。既存の SQL は適用履歴として保持します。 |
| [performance/](performance) | 負荷と待ち時間の検証。account は上限を設けた読み取り負荷の入口です。 |
| [security/](security/README.md) | 検査の合否条件と schema。アプリの機能設定とは分けて管理します。 |
| [tests/](tests) | サービスをつなぐ結合試験。実環境が必要なものは明示的な有効化条件を確認します。 |
| [tools/](tools/README.md) | CI から呼ぶ Go ツールと、共有ツールの利用手順。 |

### 本文にコメントを入れないファイル

JSON などの形式、埋め込み・checksum、既存 reader の文字コードの前提を保つため、役割はここで説明します。

| ファイル | 役割 |
| --- | --- |
| [notification-migrations/0001_create_notifications.sql](notification-migrations/0001_create_notifications.sql) | Notification の保存テーブルを最初に作る migration です。埋め込まれて実行される履歴なので、説明は README に置きます。 |
| [order-migrations/0001_create_orders.sql](order-migrations/0001_create_orders.sql) | Order の保存テーブルを最初に作る migration です。埋め込まれて実行される履歴なので、説明は README に置きます。 |
| [payment-migrations/0001_create_payments.sql](payment-migrations/0001_create_payments.sql) | Payment の保存テーブルを最初に作る migration です。埋め込まれて実行される履歴なので、説明は README に置きます。 |

ファイルの説明範囲と除外理由は [棚卸し記録](docs/file-guide.md) を参照してください。
