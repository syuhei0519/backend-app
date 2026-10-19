# Handoff

## 目的

- 対象: [backend-app #15](https://github.com/syuhei0519/backend-app/issues/15)、B系統第2工程。Refs #15（IssueはCloseしない）。実装、必要なCloud自己検証、日本語Draft PRまで。
- branch: `feat/15-order-account-tracing`。固定開始main: `0a55a61e55badebc2e1fb688270c7a21b315a815`（PR #57 merge）。初回固定実装: `f450dec65bd7ea48a2e1f03b67667faf90e79789`、初回後続はHandoff文書。独立Review後の限定修正SHAと結果は末尾追記。
- 正本: ai-development-system `42521c0bd2a1220ef01ed009455a9372708f8314` の `agents/implementation.md`、`workflows/single-issue.md`、`templates/handoff.md` を確認。checkoutとworkspaceの `.agents/skills` は存在せず追加技能なし。
- 最初に固定mainの `docs/handoff-order-account-http.md` と `docs/order-account-communication.md` を読んだ。#7が成立させた作成→存在確認→Account GET→DB、W3C/request ID、2秒child/5秒全体budget、失敗時no-writeと既存GET/Payment維持を入力として利用した。Account側のserver/DB計装・OTLP exporterを再利用し、Orderで不足していたserver/client spanとexportだけを追加した。
- 必要AC: 成功の同一traceと親子関係、下流error/timeoutの失敗statusとlatency、機密非出力、Collector停止中もAPI継続と回復後export。対象はOrder作成→Account→DBの1経路。

## やったこと

- `internal/ordertrace`: Order専用の `service.name=order-backend`、source SHA、instance UUID、`deployment.environment.name=local` / `k8s.namespace.name=order`。Collector設定時は40hex source SHAとidentityを要求。送信先は明示HTTPの既存Collector DNS:4318またはloopbackのみ。未設定は外部exportなし。
- `cmd/order-server`: migrate分岐後に初期化、既存handler/clientへtracerを注入、終了時flush。Account metricsや他サービスを再計装しない。
- `internal/orderhttp`: POST `/api/orders` のserver span。W3C extract後に開始し、client/業務層へ渡す。固定method/route、応答statusのみ。5xxでError。GET/Payment/probeは追加計装しない。
- `internal/accountclient`: 2秒child context内でclient spanを開始し、そのspanをinject。body読了・validationまでlatencyに含める。404/5xx/不正応答/timeoutは固定 `error.type` とError status。生の例外イベントや下流応答本文は記録しない。既存のretryなし/redirectなし/keepaliveなしは維持。
- `tests/distributedtrace`: 既存platform固定Collector imageを使う使い捨てfixture。memory_limiter→resource filter→batch→file exporterのOTLP JSON Linesを実際に取得する。新Secret/SaaS/恒久UIなし。親子/同一trace/時間/失敗status/属性allowlistを読み出して検証し、`go test -v` が人向けtraceツリーを出す。
- `.github/workflows/ci.yml`: 通常validate内でこの狭い実export回帰を実行し、合成OTLP JSONとcase mapを7日artifactに保存する。既存required check名/security/release条件/依存pinは維持。unitも既存 `go test -race ./...` に自然に含まれる。

送信予算: Orderと既存Accountの各SDKは非同期queue256 span、最大batch64、待機1秒、export timeout3秒、retryなし、混雑時drop可（blocking optionなし）。Order shutdownは別contextで5秒。samplingはParentBased(AlwaysSample): 親の判断を継承し、rootは100%。本試験は6固定要求・最大24span程度の少量。運用で無制限trafficを採用する承認や全span保存の保証ではない。Collectorは192MiB/増分48MiBのmemory_limiter、batch64/1秒、ローカルfileへ出力。追加の送信queue/retryや外部通信は持たない。

既存platformのtrace pipelineにはaccount限定filterとnopがあり、そのままOrderを送っても閲覧成功にはならない。本fixtureだけはaccount/orderを許可してfile sinkを使う。platform/shared metrics/manifestを変更せず、local overlayのNewRelic=trueを稼働成功と扱わない。実clusterで使う際のfilter/sink変更は別の承認範囲。

## 観察結果

2026-10-19、Cloud Go 1.27.1 / ローカルDockerで自己検証した。実Account handlerをloopback HTTP、Order実binaryを別OS process、Account/Orderを別の専用PostgreSQL containerで動かした。Accountの独立OS processは含まない。

再現は固定実装commitをcheckoutして以下を実行する。Cloudでは `source /workspace/.cloud-setup/activate.sh`、一般環境では同Go版・依存・ローカルDocker socketを用意する。container pullは下記digestだけ（Postgresは既存と同じ）。実Account/共有DB/実clusterへの接続指定は受け付けない。

```sh
git checkout f450dec65bd7ea48a2e1f03b67667faf90e79789
docker pull postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f
docker pull ghcr.io/open-telemetry/opentelemetry-collector-releases/opentelemetry-collector-contrib:0.161.0@sha256:b5cf983651c32c3ca13f936deb51742015a54d121f388cac248923ddeb8cc9fc
go test -race -count=1 ./internal/accountclient ./internal/orderhttp ./internal/ordertrace ./internal/order ./internal/orderconfig ./cmd/order-server
go vet ./internal/accountclient ./internal/orderhttp ./internal/ordertrace ./cmd/order-server ./tests/distributedtrace
go build -o /tmp/issue15-order-server ./cmd/order-server
bash ci/github/test.sh
ORDER_DISPOSABLE_TEST=1 TRACE_EVIDENCE_DIR=/tmp/issue15-traces go test -count=1 -v ./tests/distributedtrace
git diff --check
```

`TRACE_EVIDENCE_DIR` を省略するとraw OTLP fileは試験後削除される。指定時は合成データの属性検査後にだけ `traces.jsonl` と `cases.json` を保存する。通常のJSON Linesなので、例えば以下でraw header/bodyを出さずspanの親子と時刻を再閲覧できる（trace IDはcase mapと照合）。専用viewerは不要。

```sh
jq -r '.resourceSpans[] | .resource.attributes as $r | .scopeSpans[].spans[] | [($r[]|select(.key=="service.name")|.value.stringValue),.traceId,.spanId,.parentSpanId,.name,.startTimeUnixNano,.endTimeUnixNano,(.status.code // 0)] | @tsv' /tmp/issue15-traces/traces.jsonl
```

| 検証 | 結果 |
| --- | --- |
| 対象race unit、vet、build、format/diff | 成功。通常CI用実HTTP境界のparent/error/privacy回帰を含む |
| CI policy/boundary | `bash ci/github/test.sh` 成功。内部のpublish文字列はmock回帰の出力であり、公開操作はしていない |
| 実export | 成功、5ケース各4span。OTLP bytesとtraceツリーから同一traceと正しい親子、正の時間、service名を検証 |
| 正常 | 201、Order書込1。Order server→client→Account server→db SELECTの4span |
| 不明Account | 422、書込0。clientはError、Account404は既存server規約通りUnset |
| 下流error | 所有Account DBだけ停止。Order503、書込0。4spanすべてError、時間を記録。DB再開後に次試験へ |
| timeout | 専用DB内のsynthetic Account tableをtransactionでlock。Order約2秒504、書込0。4spanすべてError、clientとDBは約2秒。rollbackで解除 |
| Collector停止/復旧 | 成功traceをflush後、所有Collectorをstop。停止中201が1秒以内（初回実測7ms）、書込1。SDK export時間を越えて待ち、同じcontainerをstart。新しい要求201と、その4spanの実file exportを確認 |
| queue/終了 | 受信を止めたloopback sinkでqueue容量を超える1024spanを追加しても生成処理1秒以内、shutdown6秒以内。有限queueは欠損可 |
| 秘密非出力 | header/body/query・合成email/item・Authorization/baggageのsentinelがOTLPに無い。resource/span属性をallowlist検査、events/linksなし。資格情報・SQL・raw errorを記録しない。Accountログはio.Discard、Orderログはerrorのみで保存しない |
| cleanup | testが所有子processをkill/waitし、作成container名だけrm --force --volumes。残存inventoryを確認。共有container/volumeへ操作なし |

固定実装commitでの最終Cloud試験は22.887秒、exportは12,315 bytes、5ケース×4span。HTTP結果は正常201/4ms、不明422/1ms、下流error503/2ms、timeout504/2.001秒、Collector停止中201/7ms、回復後201/4ms。最終race unitも全対象成功。

実exportから読んだ正常trace（合成要求のtrace ID: `893ba791362a4ab1816f39691f930aeb`）:

```text
order-backend POST /api/orders          3.542ms span=1145b294af9c4989 parent=1111111111111111
  order-backend GET /api/accounts/{id}  1.416ms span=b3ab0b6dced353d6 parent=1145b294af9c4989
    account-backend GET /api/accounts/{id} 0.862ms span=2333d5b2123caeb6 parent=b3ab0b6dced353d6
      account-backend db SELECT         0.704ms span=034ba090d8260673 parent=2333d5b2123caeb6
```

timeout trace `43d783761e734afea19f3c37a74df2d1` は同じ4段構造、各span Error（OTLP code=2）。Order server/client/Account server/DBは2000.298 / 2000.208 / 2000.248 / 2000.163ms。cancel時はAccountがclient終了直後にspanを閉じ得るため、両サービスの終了時刻の完全な包含を要求しない。回復後trace `568c12805f6840989d82986583658a7b` も4spanで成功。source resourceは両サービスとも固定実装SHAを使用。停止中のtraceは欠損を許容する。

失敗履歴: 初回Collector起動はcheckoutファイルのmode600によるpermission denied。試験用コピーへmode指定だけではCloudのumaskで600となるため再度起動確認が失敗した。明示chmod0644を追加し成功。queue試験はhttptestのhandlerをrequest cancelだけで待たせたためCloseが滞留し、当該所有test processを停止。明示release channelをdeferで閉じる修正後に成功。これらはfixtureの起動/cleanupの失敗であり、成功実測に上書きしない。

未検証: 独立Review、現PR headのrequired/security CI、Kubernetes DNS/NetworkPolicy/実imageID/既存Collectorへの受入、正式GHCR公開、mergeとIssue全体Close。新しい正式公開/K8s受入をこのCloud能力追加の前提にしない。停止中のspan再送・無損失は保証せず、復旧後の新規要求のexportだけを確認した。

## 学び

#7のW3C伝播だけではOrder自身の処理区間を見られない。Order server span開始後にclient spanを作り、そのcontextをinjectすると、既存Account/DB計装が変更なしで1本につながった。timeoutはheadersまででなくbody・DBのcancelにも伝わり、約2秒の4spanで見える。

Collectorの受信成功やソースoverlayのNewRelic=trueだけでは閲覧証拠にならない。既存固定Collectorのfile exporterで実OTLP bytesを読み、parent IDを照合する方式で無料・一時的に必要ACを観察できた。file exporterはalphaであり、本番の可視化基盤や耐久保存としては提案しない。

次はManagerが独立Reviewと最新head CIを確認しmergeを判断する。Draft PRの自己検証は独立Reviewの代替ではない。main push/merge、approval代行、dispatch/rerun、公開、実cluster、Secrets/Ruleset/権限変更、追加API課金は実施していない。

### PR #58 独立Review後の限定修正

独立Reviewから、初回headの通常実trace/Collector停止復旧はPASS（12,335 bytes、停止中201/8ms）だが、Order exporter初期化前の暗黙env解析でstderrに機密値が出得るP2 blockingを受領した。初回のtrace属性検査ではこの初期化stderrを検証していなかった。既存の成功記録はその範囲の証拠として保持し、修正結果をここに追記する。

修正commit: `a7510f9de6c07c8abfd580d5bd74e74b8e62bde0`。`internal/ordertrace/trace.go` で、pin済みSDK v1.47.0がoptions適用前に読む汎用/trace用のendpoint override、headers、TLS、compression/protocol、timeout設定を、`otlptracehttp.New` 呼出し前に拒否する。許可する明示 `OTEL_EXPORTER_OTLP_ENDPOINT` は従来の検証を維持。拒否時は値やkeyを含まない `unsupported Order telemetry environment` だけを返す。環境変数を削除/変更せず、SDK internal loggerを無効化せず、metrics/logsの別signal設定やAccount/Paymentを変更しない。export無効時はSDK parserを呼ばない。

運用上、Orderで明示endpoint以外のSDK用exporter設定を追加しても、黙ってoverrideせず起動を拒否する。これは当初の「暗黙headers/TLSを受け付けない」契約をSDK parserの前で確実にする変更。新しい認証設定の導入は今回の対象外。

fresh subprocess回帰は `internal/ordertrace/trace_test.go` に追加。SDK global logger/error handlerがまだ変更されていない別processごとに、合成の不正 `OTEL_EXPORTER_OTLP_TRACES_HEADERS`、不正 `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`、正常設定を試す。前2件はsanitized errorだけをstderrに出し、secret marker/raw hostがstdout/stderrに無いことを確認。正常設定はstderr空かつloopback receiverが実OTLP bytesを受け、trace bytes/headersにもmarkerが無いことを確認する。失敗時も捕捉したraw出力をテストログへ流さない。

```sh
git checkout a7510f9de6c07c8abfd580d5bd74e74b8e62bde0
go test -race -count=1 -run 'TestInitEnvironmentSubprocess|TestDestinationAndOrderIdentity' -v ./internal/ordertrace
go vet ./internal/ordertrace
ORDER_DISPOSABLE_TEST=1 TRACE_EVIDENCE_DIR=/tmp/issue15-traces-initfix go test -count=1 -v ./tests/distributedtrace
git diff --check
```

fresh subprocess 3ケースとdestination/identity回帰は成功（package 4.060秒）、vet/diffも成功。実Collector再確認の最終値は下記追記。全機能回帰は繰り返さず、既存の6固定要求だけの狭い実export試験を利用する。

学び: `WithHeaders({})` のような明示optionsは送信設定を上書きしても、その前に走るSDK env parserのログを防げない。export後の属性検査に加え、fresh processで初期化stdout/stderrを捕捉する必要がある。新しい独立Reviewと現head CIの確認はManager担当。追加の実cluster/公開/merge/Secrets/権限操作はしていない。

限定修正SHAでの実export再確認: 23.436秒で成功、12,355 bytes、5ケース各4spanの同一trace/親子/時間/属性検査が成功。HTTPは正常201/4ms、不明422/1ms、下流error503/1ms、timeout504/2.001秒、Collector停止中201/7ms、復旧201/3ms。成功trace `f11bb8940f2449ac840ec43348eb73fc` と復旧trace `ab2ab298179a43a9a06b3209795603d6` を実fileから確認した。失敗時書込0、機密属性なし、作成container/processはcleanupし残存なし。この修正作業中のテスト失敗はなし。初回独立Reviewのstderr漏出指摘は上記修正の起点として残す。PRは [#58](https://github.com/syuhei0519/backend-app/pull/58) のままDraftで、再Review/現head CIは未確認。

### 2回目の限定修正: queue fixtureの継承env隔離

Managerから、元のstderr機密漏出P2は独立再検証で解消した一方、head `915c6da819aa2baf28425772e6ab7eabda29f56d` の両image CIがqueue試験で `unsupported Order telemetry environment` により失敗したと受領した。queue fixtureが継承OTEL設定を隔離せず、追加したproduction guardを呼んだのが再現可能な原因。CIで実際に継承された具体的な変数は未特定であり、下記再現用の変数がCIにもあったとは断定しない。初回・1回目修正の成功証拠と失敗履歴をそのまま保持する。

修正commit: `030bb811437faeda1480189ab9e91614abcdd22a`。変更は `internal/ordertrace/trace_test.go` のみ。`TestFullQueueDoesNotBlockAndShutdownBudget` の冒頭で、継承 `OTEL_EXPORTER_OTLP_*` をfixtureのtestスコープだけ `t.Setenv` で空にし、その後fixture所有loopback endpointを設定する。終了時はGo test cleanupで元の値へ戻る。実環境のVariables/Secretsの削除、production guardの無効化、productionの環境値変更は行わない。

追加回帰 `TestQueueFixtureWithInheritedExporterEnvironment` は、競合するtrace endpoint・不正header・不正timeoutを合成で与え、子testで既存queue/終了予算試験を実行する。試験後は親testの設定が復元されていることも確認する。実際の設定値はログへ出さない。

```sh
git checkout 030bb811437faeda1480189ab9e91614abcdd22a
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:4318 go test -count=1 -run '^TestFullQueueDoesNotBlockAndShutdownBudget$' -v ./internal/ordertrace
go test -race -count=1 -v ./internal/ordertrace
go vet ./internal/ordertrace
git diff --check
```

結果: 継承env付きの再現コマンドは成功（5.008秒）。通常queue、fresh subprocess 3ケース、継承env隔離/復元を含むpackage raceは成功（14.110秒）。vet/diffも成功。queue生成は1秒以内・終了6秒以内の既存assertを維持。今回の修正作業中のテスト失敗はなし。

実HTTP/DB/Collectorの成功証拠は変更のないproduction実装 `a7510f9de6c07c8abfd580d5bd74e74b8e62bde0` の23.436秒/12,355 bytesの検証を再利用し、全実試験は繰り返さなかった。新しいcontainerは作成していない。現headのrequired/image CIと独立Reviewは未確認でManager担当。PR #58をDraftのまま更新し、merge/公開/dispatch/rerunは行わない。
