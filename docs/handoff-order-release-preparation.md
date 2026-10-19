# Handoff

## 目的

担当 C: backend-app #34 → application-manifest #20 の正式公開準備。
System 正本: syuhei0519/ai-development-system@42521c0bd2a1220ef01ed009455a9372708f8314。
対象固定 SHA は backend-app `02b1018adaed8352a4651dd7fa8c73437e230296`、
application-manifest `a4fe78845e2ab1e19d593b8511ca6f28fcd206b3`。
2026-10-19 の read-only 確認で両 main と一致した。
保存先は backend-app の `docs/order-release-preparation-c` branch / Draft PR #56。
初回は公開準備文書のみだったが、後続指示により publish/propose の status 条件と
狭い回帰テストを追加した。変更ファイルと検証は末尾追記を参照。
固定 commit は PR head で確認する。manifest 値変更、Order 有効化、実クラスタ操作、
マージは行っていない。#34/#20 は未完了。

## やったこと

既存 `ci/github/release.py`、`.github/workflows/ci.yml`、
application-manifest の `trusted-manifest.yml`、Order values と consumer 契約を調査した。
既存 AC/security CI は変更していない。routing 担当との共通ファイル変更は不要。

### Manager に返す公開対象

- Order GHCR package: `ghcr.io/syuhei0519/backend-app-order`。
- Order image tag: `02b1018adaed8352a4651dd7fa8c73437e230296`。
  immutable digest は未取得。正式公開済みとは主張しない。
- source: `https://github.com/syuhei0519/backend-app`、上記固定 SHA。
- 送信物: 既存 checked OCI archive の image、GitHub Release の
  `release-record.json`、`sbom.cdx.json`、`image-scan.json`。
  release tag は `gha-order-<sourceSHA>-<run>-<attempt>`。
  公開 summary のみを使用し、private raw scan、認証情報、内部ログは含めない。
- publish matrix は Account/Order 両方。Account package `ghcr.io/syuhei0519/backend-app`
  と同じ source SHA tag も対象になり、Account v1 release と canonical evidence が生成される。
  Account 配信 values は変わらない。Order 単独 publish は既存 workflow にはない。
- `ghcr-publish` environment は reviewer `syuhei0519` と main branch policy。
  `contents: write`/`packages: write` は既存 job 権限。
  `ENABLE_GHCR_PUBLISH == 'true'`、protected main push、validate/image/ci-result が必要。
  変数 API は 403 のため現値は未確認。新 Secret/PAT/権限は作らない。
- proposal は別の `manifest-update` environment と既存 token、
  `ENABLE_MANIFEST_PR` に依存する。既存 helper の PR は英語かつ非 Draft のため、
  自動 proposal の採用を今回の日本語 Draft PR 契約と同一扱いにしない。

### 実行候補と停止点

現行 CI の `workflow_dispatch` は入力なし。ただし workflow の publish/propose 条件と
Python helper は **push event のみ** を許可するため、以下の dispatch では公開できない。

```sh
# 現行仕様の確認用。今回未実行、公開コマンドではない。
gh workflow run ci.yml -R syuhei0519/backend-app --ref main
```

既存 protected main push run の再実行は、コード変更を伴わず同じ source に対して
公開条件を再評価する候補。ただし新 attempt の検証/build が走り、両サービスが対象。
これは Manager の具体的指示前には実行しない。

```sh
# Manager が source/main 一致、変数、両 package の副作用を確認し承認した場合の候補。
# 今回未実行。新 attempt の source/run/scan/SBOM 全対応を改めて確認すること。
gh run rerun 39142519467 -R syuhei0519/backend-app
```

skip 済み publish のみを既存 artifact で再開する承認済み経路は確認できなかった。
古い attempt の artifact を新 attempt に付け替えたり、helper をローカル実行して
環境ゲートを迂回したり、freshness 判定を緩めたりしない。
変数が false/未設定なら再実行でも公開されない。変数/ゲート変更は担当外。
既存公開済み Order が別途確認できれば、その正式証拠を先に検証し再公開を増やさない。

## 観察結果

最新 main CI: https://github.com/syuhei0519/backend-app/actions/runs/39142519467
（2026-10-18、push、attempt 1）。validate、image (order)、image (backend)、ci-result は success。
publish/propose は skipped。pending deployments は空。75 件の run 一覧に実行中はなかった。
release 一覧は Account の旧 v1 2 件のみで、Order release はなかった。
これだけでは GHCR package/tag の不存在は証明できない。

checked-image-order-1 と checked-image-1 は metadata 上 unexpired、
期限はそれぞれ 2026-10-25T15:58:28Z / 15:58:31Z。
published-evidence-order-1 / published-evidence-1 はなかった。
artifact の保存期限とは別に record/scan は最大24時間の freshness が必要。
具体的 validUntil/dbUpdatedAt は取得できていないため再利用可能とは断言しない。

再現コマンドと結果（2026-10-19、上記固定 source）:

```sh
# backend-app
python3 -I ci/github/order-release_test.py             # 11 tests OK
python3 -I ci/github/release_test.py                   # 8 tests OK
python3 -I ci/github/fixtures/m0/release_test.py       # 19 tests OK
# application-manifest
python3 -I ci/github/release_test.py                   # 19 tests OK
```

合計57テスト成功。外部送信は mock による合成検証で、実公開成功ではない。
`python3 -I ci/order_inventory_test.py` は必要な `.rendered/synthetic-order/*.yaml`
未生成で FileNotFoundError。Helm がなく、実 render/security 全体は未検証。
`go version` はインストール済み `/usr/bin/go` が期待する Go toolchain と異なり失敗。
必要な Go/Helm の追加導入や全面再buildは本ドキュメント変更には行わなかった。

読み取り再現:

```sh
gh api repos/syuhei0519/backend-app/actions/runs/39142519467/jobs --jq '.jobs[] | {name,status,conclusion}'
gh api repos/syuhei0519/backend-app/actions/runs/39142519467/pending_deployments
gh api repos/syuhei0519/backend-app/actions/runs/39142519467/artifacts --jq '.artifacts | map({name,expired,expires_at})'
gh api repos/syuhei0519/backend-app/releases --jq '.[] | {tag_name,target_commitish}'
gh api repos/syuhei0519/backend-app/environments/ghcr-publish
gh api repos/syuhei0519/backend-app/environments/ghcr-publish/deployment-branch-policies
gh api repos/syuhei0519/backend-app/actions/variables
```

最後の変数読み取りは GitHub App 権限による HTTP 403。権限変更・再試行はしていない。
`gh run download ... -n checked-image-order-1` は artifact storage redirect 先で403。
GitHub connector は ZIP 参照を返したが、Cloud からその一時参照も403。
匿名 GHCR token/manifest 読み取りも403。不存在と解釈しない。
一時署名 URL、認証値、ダウンロード先の内部 ID/ログはここに保存していない。

### #20 の次工程

正式公開後、Order/Account 同一 run/attempt の canonical records、source-scan 一致、
異なる image/config/archive、Order tag/labels、SBOM/scan/checksum と有効期限を確認する。
公開済み record に従って application-manifest `environments/local/order.yaml` の
image tag/digest と release source/run/record/SBOM 対応のみ更新する。
`enabled: false` を保持し、routing/共通 workflow は触らない。
日本語 Draft PR を作成し、その head/base を固定して通常 CI を確認する。
trusted workflow は protected main のコードで PR を検証する。

```sh
# PR 番号が確定し Manager が具体的に指示した後。今回未実行。
gh workflow run trusted-manifest.yml -R syuhei0519/application-manifest --ref main -f pr=<Order値更新PR番号>
```

入力は `pr` のみ、environment は `manifest-verification`、reviewer は `syuhei0519`。
PR 番号も正式 digest も未確定のため現時点で dispatch 完成値は提示できない。
対象 verifier/source/base/head revision を記録し、report の15分鮮度と release の期限、
最新 main/run 条件を維持する。disabled render で Order が provenance inventory に出るかは
実 run で未確認であり、Trusted 成功だけで enabled Order の検証としない。

## 学び

現行公開経路は main push と既存承認を明示的に要求し、手動 dispatch と別。
artifact 保持中でも freshness/run attempt の binding により単純再利用はできない。
Manager は公開条件の未確認値と両サービスの副作用を確認し、既存再実行か限定設計変更かを
具体的に決める必要がある。新しい dispatch 公開経路の追加は scope 拡大として報告し、
独断で実装しない。自己検証のみ実施。独立 Review、現在 PR CI の評価、マージは Manager 担当。
公開・#20 digest 更新は依頼の明示停止点で pending、拒否された実行や不確かな公開結果はない。

### 追記: 承認後の指定 rerun 1 回（2026-10-19）

Manager より、ユーザーの Order/Account 両 package の公開承認と
`ENABLE_GHCR_PUBLISH` 設定完了の申告を受領した。設定値の独立確認とは区別する。
実行前の read-only 確認で backend-app main は引き続き
`02b1018adaed8352a4651dd7fa8c73437e230296`、branch protected=true。
run 39142519467 は同 SHA の main push / attempt 1 / completed success。
直近100件の run に非完了 run はなく、重複実行なしを確認した。

指示された以下を **1回だけ** 実行し exit 0、新 attempt 2 の開始を API で確認した。

```sh
gh run rerun 39142519467 -R syuhei0519/backend-app
```

結果: https://github.com/syuhei0519/backend-app/actions/runs/39142519467/attempts/2
は 2026-10-19T10:36:55Z に completed/success。
classify、validate、image (backend)、image (order)、ci-result は success。
**publish/propose は skipped のまま**。pending deployments は空で、
人間承認待ちの publish job は発生しなかった。承認の代行はしていない。
checked-image-order-2 / checked-image-2 / security-results-2 の metadata は存在し、
published-evidence-order-2 / published-evidence-2 はない。release 一覧も以前の Account
v1 2件のみで、今回の正式 Order release は生成されていない。

workflow job の publish 条件は、push / main / ref_protected /
`vars.ENABLE_GHCR_PUBLISH == 'true'` と必要 job 成功。
必要 job の成功は確認したが、評価時の ref_protected/vars の値は job metadata にない。
branch API の protected=true は当該式の評価値を直接証明するものではない。
したがって、skip の直接原因を「変数が false」等と断定しない。
変数読み取り403は既知のアクセス制限として保持し、別権限・別環境・認証で
迂回していない。403 artifact/GHCR 経路も再試行していない。
公開 helper は未実行のため、image/SBOM/scan/release の送信は発生していない。

Manager への阻害報告: 対象は同 run / attempt 2 の publish job。
公開実行はアクセス拒否や不確かな結果ではなく、既存 workflow 条件による skipped。
再 rerun はしておらず、今後の再実行・設計変更は新しい具体指示待ち。
Manager 側で既存変数の適用 scope・名前・値と run の評価条件を確認する必要がある。
Secrets/Ruleset/権限/変数/workflow は変更していない。
正式 digest がないため application-manifest #20 の disabled digest 更新も未着手。
この追記は過去の未実施記録を保存し、その後の実行結果を結ぶもの。

### 追記: skip 伝播の診断と限定修正

目的: Manager 指示により、既存公開ゲートを保持して正常な docs skip が
publish/propose を止める依存条件の不具合候補を修正する。同じ Draft PR #56 にまとめる。
過去の attempt 1/2 の skipped 結果は上記のまま保持する。

観察結果の補足: Manager はユーザー提供の非公開画面から repository variables の
`ENABLE_GHCR_PUBLISH=true`、`ENABLE_MANIFEST_PR=true`、Environment variables なしを
確認した。設定場所/値の誤り仮説は除外する。画像は Git に保存していない。
attempt 2 API は push / main / 指定 source SHA、現在 main は同 SHA / protected=true。
公開 job の steps と check summary は空。job log 取得は storage 側403で停止し、
迂回していない。実評価時の `github.ref_protected` / vars の値は未取得。

[公式の再実行仕様](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/re-run-workflows-and-jobs)
は元 SHA/ref と起動者権限の保持を規定する。一方、今回参照した公式資料には
再実行が repository variables の旧値を保持するという根拠はなく、旧値固定を原因とはしない。
[公式needs仕様](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idneeds)
と [status関数仕様](https://docs.github.com/en/actions/reference/workflows-and-actions/expressions#status-check-functions)
では skip の依存チェーン伝播と、status関数のない if への暗黙 `success()` を説明している。
`docs skipped → ci-result(always) success → publish skipped` は
[actions/runner #2205](https://github.com/actions/runner/issues/2205) の報告と同型。
これを最有力の不具合候補とするが、実 scheduler の式評価値による原因確定はしていない。
単なる新pushや文書だけのmergeは解決策としない。

やったこと（この追記時点のコード変更）:

- `.github/workflows/ci.yml`: publish の if に `!cancelled()` と
  validate/image/ci-result 各 result=`success` を追加。propose は `!cancelled()` と
  publish result=`success` を追加。既存 push/main/ref_protected/各ENABLE変数、needs、
  Environment、matrix、権限、security checks、Python 側公開拒否は維持。
- `ci/github/release_test.py`: 実 workflow の対象 job の folded if を読み、限定された
  AND/比較/boolean/status構文を評価する4契約テストを追加。未知構文/形式は失敗する。
  必要jobの failure/skipped/cancelled、docs-only、非push/非main/保護なし/変数false・未設定、
  workflow cancel を拒否。既存Environment/権限/matrix/needsも検査。
  式をコピーした独立の公開判定は作らず、既存CI harnessからそのまま実行される。
- `docs/handoff-order-release-preparation.md`: 目的更新と本追記。

Cloud 自己検証:

```sh
python3 -I ci/github/release_test.py                   # 12 tests OK（既存8 + 新規4）
python3 -I ci/github/order-release_test.py             # 11 tests OK
python3 -I ci/github/fixtures/m0/release_test.py       # 19 tests OK
git diff --check                                      # success
```

計42テスト成功。Cloud の既存 PyYAML による実 workflow YAML parse も成功。
テスト自体は標準ライブラリのみで追加依存はない。
さらに実 workflow 文字列から status関数、各needs成功、push/main/protected、各変数の
ゲートをそれぞれ1つ除いた10通りのメモリ内 mutation はすべて契約テストが拒否した。
変更前テキストや自己作成のfixture式だけを検証したものではない。

学び・未確認点: この契約テストは GitHub scheduler を実装/再現するものではない。
merge後の新 protected main push で公開 job が Environment 承認待ちまで到達するかが
最終の実 scheduler 確認であり、未検証。必要なレビュー後の merge は Manager が担当。
失敗/skip/cancel の必要jobを `always()` 単独で通す変更はしていない。
追加 rerun/dispatch/main変更/実公開/設定変更は行っていない。

workflow の修正mergeで新しい source SHA が生じるため、上記旧sourceの image/tag/digest
を今後の正式成果物として確定しない。Manager は merge後の main SHA/run/attempt を再固定し、
両 package と image/SBOM/scan/release記録という既存承認範囲のまま最終結果を確認する。
旧 attempt の artifact は新 source/run に付け替えない。正式公開後まで #20 の disabled
values 更新は保留。独立Review・現在CI確認・マージ・人間Environment承認は担当外。
