# cmd の見方

サービスごとの実行ファイルの入口。設定の読み込み、起動、終了を担当します。

## 構成と入口

この階層のディレクトリと役割は次のとおりです。末端のファイルは親の説明から追えます。

| 場所 | 役割 |
| --- | --- |
| [notification-server/](notification-server) | Notification サーバーと migration コマンドの入口。 |
| [order-server/](order-server) | Order サーバーと migration コマンドの入口。 |
| [payment-server/](payment-server) | Payment サーバーと migration コマンドの入口。 |
| [server/](server) | Account サーバーと migration コマンドの入口。 |
