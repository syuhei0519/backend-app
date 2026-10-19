# Handoff

## 目的

- 対象: `syuhei0519/backend-app` [#7](https://github.com/syuhei0519/backend-app/issues/7)。B系統第1工程のImplementation/必要Cloud自己検証のみ。
- branch: `feat/order-account-http`、[日本語Draft PR #57](https://github.com/syuhei0519/backend-app/pull/57)。Refs #7。
- 固定開始: backend `76273c5201725cd16cdf4500fcccb77c373ebcf7`、application-manifest `de2d6238d51674998fdf94436211dc8ba38ee843`、platform-gitops `3f05f580253714d2580e11a6ee75339a469c6df3`。
- 固定実装commit: [`7c7a32b1f19626d48ef73f3da5fb07becb775561`](https://github.com/syuhei0519/backend-app/commit/7c7a32b1f19626d48ef73f3da5fb07becb775561)。この後のHandoff追加commitは文書のみ。PR headの完全SHAはPRで確認する。
- 正本: ai-development-system `42521c0bd2a1220ef01ed009455a9372708f8314` の `agents/implementation.md`、`workflows/single-issue.md`、`templates/handoff.md` を確認。checkout `.agents/skills` は存在せず追加技能なし。
- AC: 有効AccountでOrder成功、不明Account/5xx/timeoutの区別、無期限待機の防止。対象はOrder作成からAccount参照の1経路。#15、Kafka、Mesh retry、Saga、全API変更は対象外。

## やったこと

主変更は「Order作成前のAccount存在確認」。[通信契約](order-account-communication.md)と[OpenAPI](order-openapi.yaml)を更新。

- `internal/accountclient/client.go`: プロセス固定の明示http origin、GET `/api/accounts/{id}`、2秒child deadline、16 KiB応答上限、redirect/proxy/retryなし。成功時は一致idを要求。下流の個人情報/エラー本文/URLを応答やログに出さない。
- `internal/order/order.go`: 検証→Account確認→DB Create、検証後5秒budget。依存未設定は作成503。HTTP中にDB transactionを保持しない。
- `internal/orderconfig/config.go` / `cmd/order-server/main.go`: `ORDER_ACCOUNT_URL` を検証して起動時にclientを一度構築。空でもmigrate/起動/既存GET/readyzは可能。非空の不正originはsanitized errorで拒否。
- `internal/orderhttp/server.go`: 作成経路でW3C contextをextract、clientでinject。安全なX-Request-IDだけ採用/生成し応答にも返す。404→422 `account_not_found`、5xx/接続失敗→503 `account_unavailable`、deadline→504 `account_timeout`、不正応答→502 `account_bad_response`、未設定→503 `account_unconfigured`。
- `internal/orderhttp/accounts_test.go` / `internal/accountclient/context_test.go`: 実HTTP stub、deadline、contextと非転送境界、失敗時no-writeを追加。
- 既存単体fixtureは明示的にAccount確認成功を与える形に変更。`tests/orderintegration/order_test.go` は旧B1の不存在ID受理をコメントに残し、新規作成422/既存参照保持を検証。Account HTTP/DB停止回復を追加。
- `tests/orderpaymentintegration/orderpayment_test.go` には新しい作成前提だけを満たすローカルAccount stubを追加し、Paymentの実API/DB回帰を維持。実Account証拠はorderintegrationが担当。

B1との意味変更: 旧契約は正の不存在IDでも201。今後は存在確認が成功しない新規作成を拒否するため、架空/削除済みIDを送るクライアントとAccount障害時の可用性に影響する。互換スイッチは追加しない。既存OrderはAccount削除後も取得可能で、既存データ削除/移行、cross-DB FK、所有者/認証検証は追加しない。照会直後のAccount削除とOrder作成は非原子的。

比較と採用理由: 既存Order→Paymentの固定origin/2秒HTTP方式を参考にした。別の検証APIや汎用frameworkより、作成前の小さなclientがACを直接満たす最小経路。下流の404はOrder自身の404と区別する422を採用。GETがstale接続で自動再送されないよう少量学習経路ではkeep-aliveを無効化した。

workflow/security/check名、migration、依存pin、manifestは変更なし。実cluster、Secret/Ruleset/権限変更、main push/merge、publish承認/dispatch/rerun、追加API課金は実施なし。

## 観察結果

2026-10-19 Cloud、Go 1.27.1、ローカルDocker 28.4.0で自己検証した。秘密値/内部ログ/非公開実行IDを本書へ含めない。以下は再現コマンドと集約結果であり、独立Test/Reviewの代替ではない。

再現環境はGo 1.27.1、ローカル `/var/run/docker.sock`、ローカルに存在する固定PostgreSQL image（pullしない）:

```text
postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f
```

このCloudの既存toolchain設定は `source /workspace/.cloud-setup/activate.sh`。一般環境では同じGo版と依存を用意する。対象repoで次を実行する。

```sh
git checkout 7c7a32b1f19626d48ef73f3da5fb07becb775561
go test -race -count=1 -v ./internal/orderhttp ./internal/order ./internal/orderconfig ./cmd/order-server
go test -race ./internal/accountclient
go vet ./internal/accountclient ./internal/order ./internal/orderhttp ./internal/orderconfig ./cmd/order-server ./tests/orderintegration ./tests/orderpaymentintegration
go build -o /tmp/order-account-server ./cmd/order-server
test -z "$(gofmt -l internal/accountclient internal/order internal/orderconfig internal/orderhttp cmd/order-server tests/orderintegration tests/orderpaymentintegration)"
git diff --check
ORDER_DISPOSABLE_TEST=1 go test -count=1 -v ./tests/orderintegration
ORDER_PAYMENT_DISPOSABLE_TEST=1 go test -count=1 -run '^TestOrderPaymentLifecycle$' -v ./tests/orderpaymentintegration
```

| 検証 | 観察結果 |
| --- | --- |
| 対象race unit/HTTP + accountclient context | 成功。既存Order strict-input/Payment routeも対象package内で維持 |
| stub正常 | 201、下流1回、DB fixture書込1回 |
| stub404 / 500・503 | 422 `account_not_found` / 503 `account_unavailable`、各下流1回、書込0 |
| 遅延header / body | 504 `account_timeout`、2.003秒 / 2.012秒、各下流1回、書込0 |
| redirect/不正・不一致・重複id/過大JSON | 502、書込0。接続失敗/未設定は503 |
| context | 有効traceparent/tracestate/X-Request-ID維持。不正context除去・request ID置換、Authorization/baggage非転送。短い親deadline/cancelを継承 |
| 実Account/Order+DB | `TestDisposableHTTPPersistenceAndAccount` 成功、32.28秒。Account実handlerをloopback httptestで、Order実binaryを別OS processで、DBを専用2 containerで実行 |
| 停止/回復 | Account HTTP listener停止→3秒以内503/書込0、同一origin再開→201。専用Account DB停止→4秒以内503/書込0、再開→201。Order既存GET/readyz維持 |
| CRUD回帰 | Account list/create/get/update/delete、409/404/400、sentinel全列とmigration履歴保持。Order create/get、削除後既存参照保持、52 invalid body/8 invalid IDの拒否/no-write、2回process再起動、Order DB再起動、migration no-opとshutdown/readiness維持 |
| 影響するPayment回帰 | `TestOrderPaymentLifecycle` 成功、18.37秒。実Order/Payment binary+専用DB 2台、Accountのみstub。成功/decline/replay/conflict/既存Order GETを確認 |
| vet / build / format / diff | すべてexit 0 |
| cleanup | `order-b1-disposable=true` ラベルのcontainer残存なし。Payment harnessのcleanupもテスト成功内で実行 |

既存APIの正常性と新契約の意図的変更を分けた。旧B1の「不存在Accountで201」は維持対象ではなく、#7で422へ変更したことをREADME/API/OpenAPI/testコメントに残した。Account providerのコード/DB schemaと既存Order GET/Payment契約は維持。

失敗履歴: 初回環境確認でPATH上の `/usr/bin/go version` はGo言語toolchainではなく `Unknown option: version`。既存activate.shでGo 1.27.1へ切替後に上記検証を実施。初期checkoutは指定開始SHAと異なり、指定SHAをfetchしてfeature branchを作成した。上記実装の実行テスト失敗・再試行による修復はなし。既存Issue/PRの過去履歴は変更/上書きしない。

未検証/残担当: 独立Review、最新headのrequired CI/security結果、実clusterのDNS/Service/EndpointSlice/NetworkPolicy/実imageID、独立Account OS processのkill、GHCR公開・人間承認、merge、issue全体完了は本自己検証で主張しない。CIの操作・rerun/dispatchは行わず、Managerが現在headを確認する。ローカルTrivyは未導入でsecurity scanは既存CIへ委ねる。本機能のCloud受入に新規GHCR公開は必要ない。

## 学び

必要3ACは上記CloudのHTTP/stub/実DB範囲で観察できた。2秒deadlineはresponse headersだけでなくbodyまで必要であり、Account存在確認を作成に入れると障害時に書込を止める契約になる。照会成功は認証/所有権/削除競合の保証ではない。

### #15へ引き継ぐ実箇所と境界

- 呼出し: `orderhttp.Server.ServeHTTP` の作成分岐 → `order.Service.Create` → `accountclient.Client.Check` → Account `httpapi.NewWithTelemetry` のGET route → `account.Service.Get` → PostgreSQL。Payment経路は今回変更しない。
- client/server context: Order作成分岐の `accountclient.Extract` がW3Cを受け、Checkがdeadline付きcontextからinjectする。Orderは現時点で新規server/client spanやexporterを持たず、受けたparentを伝播するだけ。Account `internal/httpapi/observation.go` はextract→server span、`internal/telemetry.DB` がDB child spanを作る。
- exporter/Service名: Account `cmd/server/main.go` → `telemetry.Init`。`service.name=account-backend`、OTLP/HTTPの `/v1/traces` と `/v1/metrics`、endpoint未設定では外部exporterなし。3秒export timeout、retryなし、batch queue256/最大64/1秒。環境/namespace/endpoint許可の既存検証を勝手にOrderへ流用しない。Order用service名とresourceは#15で設計する。
- 機密境界: 現Account計装はroute template・status・時間・trace/span IDだけ。SQL/引数、本文、メール、資格情報、raw error/URL、Authorization/baggageをspan/logに追加しない。request_idはtrace_idと独立。
- sink現状: Managerからのlocal調査共有は「OTLP4318受信可、traceはnopで破棄、閲覧sinkなし、GrafanaはPrometheusのみ」。実clusterは本担当未読/未実測。固定platform SHAのソースにはdefault `newRelicEnabled:false`（nop）に加えlocal overlayの`true`（外部exporter選択）もあるため、ソース設定だけで稼働中sinkの成功を断定しない。trace pipelineはmemory_limiter/filter(resource contract)/resource defaults/batchを経る。namespace `account` / environment `local` のfilterに注意。Secret内容は未読。
- 推奨少量3case: (1)有効Accountで201・同一trace IDのOrder server/client→Account server/DB parent関係、(2)不存在で422・Account404とOrder書込なし、(3)stub遅延で約2秒504・timeout spanと後続DB書込なし。#15はCloud内の使い捨てCollector/sinkで秘密なし・追加課金なしの検証を検討し、今回Collector再導入や大量trace改修を先取りしない。

### runtime権限・復旧と次の担当

本Cloud検証は作成したloopback listener/子process/ローカルDockerの専用containerだけを操作し、外部実サービスを停止/負荷試験しない。復旧は所有process停止とexact container名+そのvolumeの削除だけ。中断後は所有するexact名を確認して `docker rm --force --volumes <exact-created-name>`。global prune、既存container/volume/DBの削除は不可。

後続runtimeにはManagerが承認した既存Order workloadのimage/config更新権限、対象Service/EndpointSlice/Pod/imageID/healthの読取、Order→AccountのTCP8080到達性（および既存Order DB接続）が必要。OrderにAccount DB資格情報は不要。`ORDER_ACCOUNT_URL` は非Secret originでありSecret変更/新RBAC/Ruleset変更は不要。正確なnamespace/workload/受入imageを別担当が確定してから操作する。本担当はその権限変更やcluster操作を実施しない。

復旧は承認範囲内のOrderの以前のimage/configだけを戻す。SQL/migration変更なしのため既存データのdown/削除は不要。旧imageに戻すとB1の不存在ID受理へ契約も戻る点を判断材料にする。Account/DB/PVC/Secret/共有namespaceは触らない。

次はManagerが独立Reviewと現head CIを別担当で確認し、必要なmanifest/実cluster受入とmergeを判断する。mainのpublishは有効で、merge後のghcr-publishは人間承認待ちになり得る。本担当は承認/dispatch/rerunしない。PRはDraft・未mergeで、Issue #7全体のCloseや統合完了を宣言しない。
