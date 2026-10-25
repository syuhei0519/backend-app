# サービスをつなぐ試験の見方

単体テストでは確認できない、HTTP・DB・サービス間通信のつながりを確認します。
実環境を使う試験は専用の有効化条件があり、通常の `go test ./...` ではその条件を満たさない試験をスキップします。
稼働中の環境を試験先にする前提ではありません。

## 構成と入口

| ディレクトリ | 役割 |
| --- | --- |
| [distributedtrace/](distributedtrace) | Order から Account へつながるトレースを隔離環境で採取し、親子関係と記録内容を確認します。保存データの検証は `validate_test.go` です。 |
| [orderintegration/](orderintegration) | Order API と専用 DB をつなぎ、Account 確認後の保存と失敗時の応答を確かめます。 |
| [paymentintegration/](paymentintegration) | Payment API と専用 DB をつなぎ、支払い結果の保存と再要求時の応答を確かめます。 |
| [notificationintegration/](notificationintegration) | Notification API と専用 DB をつなぎ、通知受付記録の保存と重複時の応答を確かめます。 |
| [orderpaymentintegration/](orderpaymentintegration) | Order から Payment への依頼を、使い捨ての専用環境で確かめます。準備と片付けは `disposable.go` にまとめています。 |

各テストの冒頭と、対応する `internal/*test/disposable.go` またはこの階層の `disposable.go` で、有効化条件と準備する資源を確認してください。
