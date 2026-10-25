# internal/store の見方

PostgreSQL の保存・migration と、サービス間で共用する DB 待機処理。

## 構成と入口

各サービスの postgres package が保存と migration、dbwait が起動時の接続待ちを担当します。

この階層のディレクトリと役割は次のとおりです。末端のファイルは親の説明から追えます。

| 場所 | 役割 |
| --- | --- |
| [dbwait/](dbwait) | DB 起動待ちの試行回数・期限・中断を共通化した処理。 |
| [notificationpostgres/](notificationpostgres) | Notification 専用 PostgreSQL への保存・取得と migration。 |
| [orderpostgres/](orderpostgres) | Order 専用 PostgreSQL への保存・取得と migration。 |
| [paymentpostgres/](paymentpostgres) | Payment 専用 PostgreSQL への保存・取得と migration。 |
| [postgres/](postgres) | Account 専用 PostgreSQL への保存・取得と migration。 |
