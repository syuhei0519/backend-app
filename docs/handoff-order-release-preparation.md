# Handoff

## 目的

担当 C: backend-app #34 → application-manifest #20 の正式公開準備。
System 正本: syuhei0519/ai-development-system@42521c0bd2a1220ef01ed009455a9372708f8314。
対象固定 SHA は backend-app `02b1018adaed8352a4651dd7fa8c73437e230296`、
application-manifest `a4fe78845e2ab1e19d593b8511ca6f28fcd206b3`。
2026-10-19 の read-only 確認で両 main と一致した。
保存先は backend-app の `docs/order-release-preparation-c` branch、変更は本ファイルのみ。
本ファイルの固定 commit は PR head で確認する。コード差分、公開、manifest 値変更、
Order 有効化、実クラスタ操作、マージは行っていない。#34/#20 は未完了。

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
