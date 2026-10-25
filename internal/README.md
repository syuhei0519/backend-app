# internal の見方

API の業務処理、HTTP、設定、保存、計測を役割別に分けた内部コード。

## 構成と入口

HTTP の入口から業務処理を呼び、保存処理は store へ渡します。サービス間の通信は client の各 package を参照します。

この階層のディレクトリと役割は次のとおりです。末端のファイルは親の説明から追えます。

| 場所 | 役割 |
| --- | --- |
| [account/](account) | Account のデータと業務処理。HTTP や DB の具体的な実装から独立させます。 |
| [accountclient/](accountclient) | Order から Account の存在を確認する HTTP client。 |
| [config/](config) | Account サーバーの環境変数と起動条件の検証。 |
| [httpapi/](httpapi) | Account API の経路、要求・応答、稼働確認、HTTP 観測。 |
| [notification/](notification) | Notification のデータ・入力条件・業務処理。 |
| [notificationconfig/](notificationconfig) | Notification の環境変数と起動条件の検証。 |
| [notificationhttp/](notificationhttp) | Notification の HTTP 要求・応答と稼働確認。 |
| [notificationtest/](notificationtest) | Notification の使い捨て DB 試験環境を準備・片付けする補助処理。 |
| [order/](order) | Order のデータ・入力条件・業務処理。 |
| [orderconfig/](orderconfig) | Order の環境変数と起動条件の検証。 |
| [orderhttp/](orderhttp) | Order の HTTP 要求・応答と稼働確認。 |
| [orderpayment/](orderpayment) | Order の存在確認と Payment への依頼をつなぐ処理。 |
| [ordertest/](ordertest) | Order の使い捨て DB 試験環境を準備・片付けする補助処理。 |
| [ordertrace/](ordertrace) | Order の分散トレースの初期化と終了処理。 |
| [payment/](payment) | Payment のデータ・入力条件・業務処理。 |
| [paymentclient/](paymentclient) | Payment への期限付き HTTP 呼び出しと応答検証。 |
| [paymentconfig/](paymentconfig) | Payment の環境変数と起動条件の検証。 |
| [paymenthttp/](paymenthttp) | Payment の HTTP 要求・応答と稼働確認。 |
| [paymenttest/](paymenttest) | Payment の使い捨て DB 試験環境を準備・片付けする補助処理。 |
| [store/](store/README.md) | PostgreSQL の保存・migration と、サービス間で共用する DB 待機処理。 |
| [telemetry/](telemetry) | Account の計測初期化と送信先・識別情報の検証。 |
