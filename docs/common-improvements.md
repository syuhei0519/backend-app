# 共通レビュー2〜5への対応

## 目的

学習に必要な結論と再現手順を残し、証跡と Issue / PR の負担を小さくする。
対象は共通指摘2〜5。個別アプリ機能や新規 Istio 導入は含めない。

## やったこと

共有 Go module の正本を tools/ に集約。module / import を GitHub へ移行。manifest 固有 release reader / inspection 利用側 と host namespace 拒否を統合。
Order→Payment の実装時点の kind 未確認を保持し、統合後の最終確認へリンク。
全5 repo で「目的・やったこと・観察結果・学び」のテンプレートを追加。
無効な `.gitlab-ci.yml` を削除し、現行 README の案内を GitHub へ更新。
過去の出典、GitLab の学習対象、互換テストの API URL は機械置換しない。

## 観察結果

`go test ./...`: 成功（実 DB の設定を要求するテストは既定の skip 条件に従う）。
3共有 module の `go test ./...`: 成功。
初回 push CI は module/import 移行後の共有 CLI の gofmt 未適用で失敗した。履歴を保持し、整形を追加した。frontend / backend の相互 source identity 誤結合を拒否。
`bash ci/github/test.sh`: 成功。
workflow YAML の構文を確認。既存 CI job 名・required result gate は保持。
再現には保存環境の `/workspace/.cloud-setup/activate.sh` 相当の Go / Helm / Python が必要。
Windows 実機での checkout は未実施。深い生成証跡は現行ツリーから除き、固定履歴で参照する。
Git history、失敗、実装時点の未実施を上書きしていない。

## 学び

CI artifact には期限があるため、対象 commit・コマンド・結論を Markdown に残す。
実際の fixture / schema / lock / vendor と、生成された運用証跡を区別して整理できる。
旧 Handoff / Delivery schema と CLI は明示的な過去契約の回帰用に残すが、新規タスクの必須記録にはしない。
別の担当者によるレビュー は未実施で 担当者 が別途担当する。マージ・Secrets/Ruleset 変更・環境配備は行っていない。
既存 Issue / PR の本文更新と139 Issueの整理は 担当者 の担当。
