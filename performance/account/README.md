# Accountの小さなk6負荷（#6）

既存Account APIの `GET /api/accounts` だけで開始する。`normal` は200だけ、
`mixed-4xx` はiteration番号の偶奇で200と `/api/accounts/0` の400を交互に送る。
POST/PUT/DELETE、setup/teardownはなく、既存データを生成・更新・削除しない。
応答本文は破棄し、一覧の内容をartifactに保存しない。smoke試験の置換・SLO gateではない。

## guardと予算

サポートする入口は `python3 performance/account/run.py`。Python3標準ライブラリとk6 **v2.3.0** が必要。
手動workflow `Account low load (manual)` に固定download/checksumを置いている。

- 確認済み専用テスト先への **`http://127.0.0.1:1024..65535` のoriginのみ**許可。
  localhost/DNS/外部IP/HTTPS/credentials/path/query/fragmentは起動前拒否。
  loopbackでも本番や無許可の転送先を使わない。対象と権限はManagerが確認する。
- rateは整数1..5 HTTP req/s、durationは整数1..60秒。既定は1 req/s、30秒。
- constant-arrival-rate、要求を送るiterationは各1 HTTP要求。duration境界でk6が余分なiterationを
  開始した場合はHTTPを送らず `budget_skipped_iterations` に記録する。`iterations` と
  `http_reqs` を同一視せず、実要求数は必ず `http_reqs` で確認する。setup=0、cleanup=0、redirect=0、
  HTTP retry=0、batchなし。最大要求数=`rate × duration`（上限300）。
  最大5 VU・各VU同期1要求のため同時接続5以下。到着スケジュールの上限であり、
  OSの遅延を含む任意の1秒区間に厳密5回以下という保証ではない。
- 要求timeout2秒、終了猶予3秒。新規要求の予定窓は60秒以下、処理完了は最大約63秒。
  VU不足のdropped iterationsは失敗。要求を追加して取り戻さない。
- k6環境override/proxyとユーザーconfigを継承せず、usage reportを無効にする。
  任意の `k6 run` CLIによる改変はサポートしない。guardはloopback転送先の実体認証ではない。
- `revision` は確認済み対象の40桁source SHA。入力値は対象に照会して検証するものではない。
  labelは公開可能な短い学習名だけ。内部実行IDや認証情報を入れない。

対象を確認したローカルTest担当が実行する例（Cloudからkindへは実行しない）:

```sh
python3 performance/account/run.py --target http://127.0.0.1:18080 \
  --revision "$CONFIRMED_ACCOUNT_SOURCE_SHA" --rate 1 --duration 30 \
  --profile normal --label learning-smoke --output .build/account-normal
python3 performance/account/run.py --target http://127.0.0.1:18080 \
  --revision "$CONFIRMED_ACCOUNT_SOURCE_SHA" --rate 1 --duration 30 \
  --profile mixed-4xx --label learning-mixed --output .build/account-mixed
```

各回の `input.json` と `summary.json` に元のprofile/rate/duration/revision/k6版/UTC時刻、
実 `http_reqs`、`http_req_failed`、応答時間ms（平均・p95等）を保存。
同じcheckout・k6版・対象revision・入力で再実行できる（出力は新しいdirectory）。
400は期待応答のcheckに成功しても**http_req_failedに含む**。200以外はHTTP失敗、
通信障害はstatus0の失敗、redirectは追跡せずcheck失敗。正常と400混在を分けて比較する。
thresholdは応答契約checkとdropped iterationsだけで、SLOによる成否判定は行わない。
異常終了時はexit codeを失敗として扱い、summary未生成を成功や0件にしない。

## CloudとCIの検証

```sh
python3 -I performance/account/test_scenario.py -v
CGO_ENABLED=0 go build -trimpath -o .build/performance/server ./cmd/server
python3 -I performance/account/isolated.py --binary .build/performance/server --output .build/account-load
```

fixture testsは境界・外部URL拒否、実k6対使い捨てHTTP fixtureの正常/400/redirect/500、
要求数・環境override拒否・guard拒否後の無通信を確認する。実DB試験の代替とはしない。
`isolated.py` は固定imageの使い捨てPostgres、migration、Account binaryだけを使う。
専用Docker bridge network、DB非公開、APIのloopback動的portのみ。外部負荷先/telemetry exporterを
設定しない（network自体はegress隔離を保証しない）。Dockerはローカルsocket固定。
既存DB/PVC/clusterには接続せず、作成したcontainer/volume/networkだけをfinallyで消す。
合成sentinel行を事前作成し、2 profile（各1 req/s・30秒、合計60要求）後に同一性を照合する。
DB操作とTCP readinessはHTTP要求を増やさず、HTTP setup/cleanupは0件。
DB/API準備失敗・cleanup失敗は非0で、skip成功にはしない。対象binaryのbuild元は呼出側の責任。

workflowは入力URLを受け取らず、この隔離構成だけを手動実行する。artifactは上記2ファイル/profileを
7日保存し、内部Docker名・アプリログ・本文は公開しない。通常CI/security/releaseは変更しない。

SLIはplatform-gitops `learning/monitoring/SLI.md` の同じ5m窓で比較する。
短い30秒負荷と5mの背景通信は区別し、UTC窓とtarget_infoの対象revisionを揃える。
k6はclient時間ms、SLIはserver時間秒であり、数値の一致やrun単位のmetric分離は保証しない。
