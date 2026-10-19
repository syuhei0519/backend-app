# Order → Account 同期HTTP通信

Refs [#7](https://github.com/syuhei0519/backend-app/issues/7)。対象は
`POST /api/orders` → `GET /api/accounts/{account_id}` の1経路。

## 契約と選択理由

B1では正のaccount_idは存在しなくても201だった。今回から入力検証後、
Accountが200と一致するidを返した場合のみOrderを書き込む。有効とはAccountの
現行CRUD上で存在することを指し、active属性や認証・所有者検証を追加しない。
旧クライアントの架空/削除済みIDによる作成は422になり、Account停止・設定不足でも
作成できなくなる。互換スイッチは設けない。既存データの削除・移行は不要。
既存OrderのGETとPayment経路はAccountを呼ばず、Account削除後も読める。
照会と作成は分散transactionではなく、照会直後の削除との競合は残る。

既存Order→Paymentは「入力検証→Order GET→2秒HTTP」の構成だった。
今回も専用の小さなHTTP clientを使い、Order作成前に存在確認を挿入した。
別の検証endpointや全APIの共通frameworkを追加するより変更点が少なく、
必要ACを利用者の作成操作で確認できる。HTTP中はOrder DB transactionを持たない。

| 条件 | Order応答 | 書込 |
| --- | --- | --- |
| Account 200 + JSONの一致id | 201（DB commit後） | 1 |
| Account 404 | 422 `account_not_found` | 0 |
| Account 5xx / 接続障害 / 親cancel | 503 `account_unavailable` | 0 |
| 下流deadline / 親deadline | 504 `account_timeout` | 0 |
| redirect / その他status / 不正・過大・不一致JSON | 502 `account_bad_response` | 0 |
| 接続先未設定 | 503 `account_unconfigured` | 0 |

既存入力validation・DB側の503/500は維持。422は存在しないOrderの404と区別する。
応答は既存のerror envelopeで固定status文言のみ。下流本文・URL・Accountの個人情報・
資格情報・driver errorを返さない。200本文は16 KiB上限、idのみ利用し、top-level
重複キー・末尾JSONも拒否する。他フィールドは将来拡張として許容する。

## 接続先・時間制限・context

`ORDER_ACCOUNT_URL=http://learning-account:8080` のような明示port付きhttp originを
プロセス起動時に固定する。userinfo・path（`/`も）・query・fragmentは不可。
未設定でもmigrate/起動/GET/readyzは使え、作成だけ503。非空の不正設定は起動拒否。
readyzは従来のOrder DBだけを判定し、Account障害で既存Orderを読めなくしない。

入力検証後5秒の親budget、下流はDNS/connect/応答本文を含む2秒のchild deadline。
Order DBの既存3秒deadlineと早い親期限を継承する。serverのread/write上限は10秒。
OS schedulingや通信配送の厳密な実時間保証ではない。proxy・redirect・自動retryは無し。
GETのstale接続再送も避けるため、この少量学習経路はkeep-aliveを無効にした。
失敗を直すための無限retryはしない。

作成routeでW3C TraceContextをextractし、clientでinjectする。
`X-Request-ID`はAccountと同じ32hex/UUID形式だけ受入、未指定/不正はUUIDを生成し、
作成応答にも返す。Authorization・baggage・任意headerは転送しない。
新しいexporter・Order span・Collectorは追加しない（#15の担当）。

## Cloud再現

Go 1.27.1、ローカルDocker socketと既存の以下digestが必要。
外部Account、共有DB、既存volumeは使わず、image pullもしない。

```text
postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f
```

```sh
go test -race -count=1 -v ./internal/accountclient ./internal/orderhttp ./internal/order ./internal/orderconfig ./cmd/order-server
go vet ./internal/accountclient ./internal/order ./internal/orderhttp ./internal/orderconfig ./cmd/order-server ./tests/orderintegration ./tests/orderpaymentintegration
go build ./cmd/order-server
ORDER_DISPOSABLE_TEST=1 go test -count=1 -v ./tests/orderintegration
ORDER_PAYMENT_DISPOSABLE_TEST=1 go test -count=1 -run '^TestOrderPaymentLifecycle$' -v ./tests/orderpaymentintegration
```

stub検証は通常/404/500/503/遅延headers/遅延bodyとcontextを実HTTPで確認する。
実DB検証は既存のfresh Account/Order別PostgreSQL harnessを使う。
Accountは実HTTP handlerをhttptest listener上で、Orderは実binaryの別processで動かす。
Account HTTPの停止/同一origin再開と専用Account DB停止/再開で503/201、失敗時no-write、
既存Order GET/readyz維持、Account CRUD/409/404/400、sentinelとmigration履歴保持を確認。
Accountを独立OS processでkillする試験やKubernetesのService/DNS/NetworkPolicy実測は含まない。

cleanupは作成した子processとexact container名・そのvolumeだけに限定する。
外部killで中断した場合は `order-b1-disposable=true` ラベルのinventoryから当該試行が
所有するexact名を確認し `docker rm --force --volumes <exact-created-name>` で除去する。
共有resourceの削除やglobal pruneは禁止。既存DB/Secretの変更も不要。
独立Review、現head CI、実cluster受入とmerge判断はManagerへ引き継ぐ。
