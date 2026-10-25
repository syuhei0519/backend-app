# tools/scan-gate の見方

各 repo の方針に従って検出結果や描画 YAML を合否判定する共有 module。

## 構成と入口

[main.go](main.go) が入口、[policy.go](policy.go) が方針の読み込み、[render.go](render.go) が YAML の危険な構成の検出を担当します。


主なファイルの入口です。

| ファイル | 役割 |
| --- | --- |
| [go.mod](go.mod) | 各 repository で使うセキュリティ判定ツールの module と依存定義です。 |
| [host_namespace_test.go](host_namespace_test.go) | Pod がホストの名前空間を共有する危険な設定を、描画結果の検査で拒否することを確認します。 |
| [image_test.go](image_test.go) | イメージの検査結果について、脆弱性や秘密の検出を方針どおりに判定することを確認します。 |
| [main.go](main.go) | ソース・イメージ・描画済み YAML の検査結果を読み、方針に沿って CI の合否を返す入口です。 |
| [policy_test.go](policy_test.go) | 検査方針の必須項目と判定条件を確認し、曖昧な設定を受理しないようにします。 |
| [render.go](render.go) | 描画済み Kubernetes YAML を読み、特権実行や平文の秘密など禁止した構成を検出します。 |
| [render_test.go](render_test.go) | 描画結果の検査が、許可する構成と禁止する構成を正しく区別することを確認します。 |
