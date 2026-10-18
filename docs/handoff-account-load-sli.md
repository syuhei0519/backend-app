# Handoff

## 目的

Lane B: backend-app#6 と platform-gitops#5。既存Accountに小さな再現負荷をかけ、
既存Prometheus/Grafanaで可用性・error・latencyを同じ学習基準で比較する。
System正本は `ai-development-system@42521c0bd2a1220ef01ed009455a9372708f8314` の
implementation/single-issue/Markdown Handoffに従った。

| repo | 取得・固定した最新main | branch | 実装commit |
| --- | --- | --- | --- |
| backend-app | dd22f325a2f67944507969d8c5ba1b291adf6e01 | feat/account-k6 | a65152e9b8d837584591f7e2f8ca86203c1a4ed8 |
| platform-gitops | 3a62ce5b087e7e2215b74028acac6b878679e40c | feat/account-sli | 687b9ec9e0d58849c8abbde4922ec52b9978c1c1 |

Draft PR: [backend #54](https://github.com/syuhei0519/backend-app/pull/54)、
[platform #89](https://github.com/syuhei0519/platform-gitops/pull/89)。
本Handoff追加commitはbackendの実装commitの後続であり、実装差分は上記commitに固定する。
担当ACは再現入力・要求数/error/latency artifact・事前guard、SLI/query/窓/目標/budget/欠測定義。
独立Review・実cluster確認・統合/mergeは完了扱いにしない。

## やったこと

- backend `performance/account/`: Python起動前guard、k6正常/400混在GET、隔離HTTP fixtureテスト、
  使い捨て実Account/Postgres検証、短いREADME。
  専用loopback originのみ、1..5 req/s、1..60秒、最大5 VU、最大300 HTTP要求。
  setup/cleanup HTTP=0、redirect/retry=0、本文破棄、データ書込みなし。
  duration境界の余分なiterationはHTTP送信前に抑止し `budget_skipped_iterations` に記録する。
- backend `.github/workflows/account-load.yml`: 固定k6 v2.3.0/checksumと既存固定Actionを使う手動CI。
  外部URL入力なし、使い捨てDB/APIのみ、入力/summary artifactを7日保存。通常CIを変更しない。
- platform `learning/monitoring/`: `SLI.md`、即時PromQL、compact fixture、既存dashboardへの7パネル追加。
  既存6 REDパネル/式と10 fixtureを保持。5m・20要求以上、可用性99%、250ms以内95%、
  4xx/5xxを含む失敗とbudget、通信なし/欠測/少数sampleを定義した。
  既存validate/check-tools hookを再利用。Manager/Lane Aによるroot CI hook追加は不要。
- 既存アプリコード、application-manifest、platform root CI、Argo/Rollouts領域は変更していない。
  API/課金追加、main push、Secrets/Ruleset/権限変更、実cluster操作、新kind構築は行っていない。

## 観察結果

Cloud自己検証（2026-10-18）:

| 確認 | 結果 |
| --- | --- |
| guard境界・外部/不正origin/rate/duration拒否 | PASS。拒否後fixtureへのHTTP増加なし |
| 実k6対使い捨てHTTP fixture | 正常/400/302追跡拒否/500、各4 HTTP要求、override無効、最大5 VU以内 |
| 実Account/Postgres 正常 | 30 HTTP要求、error 0%、check 100%、client p95 約4.317ms |
| 実Account/Postgres 400混在 | 30 HTTP要求、error 50%、check 100%、client p95 約4.919ms |
| データ/cleanup | 合成sentinel行の同一性保持。作成container/volume/networkを削除。試験後Docker稼働containerなし |
| SLI PromQL | 固定Prometheus promtoolで17ケースPASS（既存RED 10ケースもPASS） |
| render/config | Helm4.2.4 strict lint、1.36.4 render、12資源境界、dashboard/query完全一致、Collector config PASS |
| Kubernetes schema | 既存CIと同じkubeconform/1.36.0で12/12 valid、invalid/error/skip=0 |
| その他 | Python compile、workflow YAML parse、両repo diff --check PASS |

再現（backend、Python3・k6 v2.3.0・Go1.27.1・Docker）:

```sh
python3 -I performance/account/test_scenario.py -v
mkdir -p .build/performance
CGO_ENABLED=0 go build -trimpath -ldflags="-X main.buildCommit=$(git rev-parse HEAD)" \
  -o .build/performance/server ./cmd/server
python3 -I performance/account/isolated.py \
  --binary .build/performance/server --output .build/account-load
```

Cloudでは固定Go imageでbuildした。最初はコンテナ内CA不足、その後Git所有者検査で失敗した。
既存host CAをread-onlyで渡し、使い捨てbuild container内だけでsafe.directoryを指定して成功した。
証明書検証や既存checkは無効化しなかった。Cloudでの成功buildコマンド:

```sh
docker run --rm -v "$PWD:/src" \
  -v /etc/ssl/certs/ca-certificates.crt:/trust/ca.pem:ro -e SSL_CERT_FILE=/trust/ca.pem -w /src \
  golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b \
  sh -c 'git config --global --add safe.directory /src && CGO_ENABLED=0 go build -trimpath -o .build/performance/server ./cmd/server'
```

Cloud測定のAPI binaryは固定mainの既存アプリからbuild（アプリコード変更なし）。
結果は `.build/account-load/{normal,mixed-4xx}/{input,summary}.json`。
再現時は上記のように対象buildCommitを設定し、同じ入力を新規output directoryへ保存する。
元のDB試行はDocker internal networkのport公開不可で失敗しcleanup済み。
専用bridge・DB非公開・API loopback限定へ修正後、60要求試験が成功した。
network自体のegress隔離は保証せず、fixtureに外部負荷/telemetryを設定しない。

再現（platform、既存monitoring用Python依存・Helm・Docker）:

```sh
python3 learning/monitoring/validate.py --output .rendered/learning-monitoring
sh learning/monitoring/check-tools.sh .rendered/learning-monitoring
docker run --rm -v "$PWD:/work:ro" \
  -v /etc/ssl/certs/ca-certificates.crt:/trust/ca.pem:ro -e SSL_CERT_FILE=/trust/ca.pem -w /work \
  ghcr.io/yannh/kubeconform:v0.7.0-alpine@sha256:8f0eeaaa96ba27ba1500b0e4b1c215acc358d159c62a7ecae58d7a03403287b0 \
  -strict -summary -kubernetes-version 1.36.0 .rendered/learning-monitoring/rendered.yaml
```

kubeconformも最初のCA不足はread-only bundleで解消。SLI reset fixtureの初期期待値は
外挿増分300を誤って仮定し失敗したため、実窓の270へ訂正し再検証した。
これらの失敗を実DB試験skipや成功へ読み替えていない。

両Draft PRの既存CIは作成時にclassify成功・validate実行中だった。required CIの最終成功は
Managerが最新headで確認する。新規の手動workflowはdefault branch未マージのためGitHub上では未実行。
artifact生成はCloudで確認済みだが、GitHub upload自体の成功は未確認。
公開Gitには内部Docker名/実行ID、内部ログ、認証情報、API本文、生成artifactを含めない。

## 学び

k6のiteration数はduration境界でHTTP要求数とずれることがある。予算はHTTP送信前に制限し、
比較は `http_reqs` とerror/latencyで行う。400を期待したprofileでもHTTP失敗率から隠さない。
5m/20 sampleの学習目標は本番SLAや自動判定ではなく、低trafficや欠測を健全に読み替えない。
現在のheartbeat鮮度は窓全体の完全性や複数instance一部欠測を保証しない。
versionはtarget_info、HTTP系列はjob/instanceにあり、run識別labelはない。
将来のrevision別joinはliveでラベル対応を確認する必要があり、今回は計装全改修しない。

残りはManager指定のローカルTest担当が実施する:

1. live試験直前にcluster/context/Account namespace・source/image/telemetryを再確認。
   専用loopback転送先と権限をManagerが渡し、既存データ・他サービス・監視を保護する。
2. guard入口で正常/400混在を別窓で実行し、Prometheus instant queryとGrafana SLI/REDを同じUTC時刻で照合。
   停止後の通信なしも確認。fixture済みの500/欠測のために実DB停止を追加しない。
3. 新dashboard/config適用は既存monitoring READMEとManagerの承認境界に従う。
   追加権限が必要なら対象/理由を返し、権限変更や迂回をしない。
4. default branch導入後に手動workflowを確認。独立Test/ReviewはManagerが別担当へ委任し、
   最新required CIと全体ACを確認してmerge判断する。実装者はmergeしない。

現時点で拒否・保留中の権限操作はない。Cloud担当範囲に未解消の阻害要因はない。
