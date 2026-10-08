# PE-012 SDK計測契約

Go OpenTelemetry SDK/OTLP HTTP exporterをv1.47.0へ固定する。SDKはbackendだけに存在し、NRキーを受け取らず、非秘密Collector endpointへ送る。OTLP endpointの受信先はlocal CollectorのHTTP4318と明示ローカルfixtureだけ、pathは/v1/metrics・/v1/tracesに固定。migrationはSDKを起動しない。

service.name=account-backend、service.version=build-time VCS_REFの40桁source SHA、service.instance.id=プロセス起動UUID（Pod UIDとは別）。namespace/Pod UIDはDownward API、environment=localはmanifestの非秘密値。有効化時はidentity不足・未知endpointを起動エラーとする。endpoint未指定の旧配備では外部送信しない。

HTTP要求は独自middlewareだけで一回計数する。pe_http_server_requests・unit1がPrometheus pe_http_server_requests_total、pe_http_server_request_duration・unitsがpe_http_server_request_duration_secondsへ変換されることをCollector0.161.0で実測済み。累積temporality、export10秒、explicit境界0.005/0.01/0.025/0.05/0.1/0.25/0.5/1/2.5/5/10秒。labelはmethod/正規化route/status_classだけで、SHAはResource/target_infoへ置く。正常性確認を業務REDから除外、404はunmatched、IDは{id}。panicを回収し未送信responseは500、一回だけ計測する。

W3C traceparent/tracestateを取り込み、HTTP server span→DB client child spanにcontextを継承する。DB spanは操作種別SELECT/INSERT/UPDATE/DELETEだけ、SQL/引数/driver error文を記録しない。ログはroute/status/duration/request_id/trace_id/span_id/version/instanceをJSONで出し、本文/email/Authorization/cookie/raw path/queryを記録しない。任意request_idは拒否し32hex/UUID以外は新規生成する。

trace batch queue256・batch64・flush1秒・export上限3秒・retryなし・非blocking、metrics export10秒/上限3秒/retryなし。両provider shutdownは共有5秒以内。export失敗counterは失敗したexportであり、全spanの最終drop数の証明には使用しない。SDKのqueue上限をCollectorのqueue32/retry30秒と混同しない。runtime goroutines/allocated bytesとexport失敗counterを別計測する。

SDK reader検証は業務6要求(counter/histogram各6)・health2除外・400/404/500/panic・既知30ms遅延・HTTP6/DB3spanとW3C親保持・機密属性/ログ不存在を確認。10,000span/measurementの非blocking要求経路と失敗export、共有shutdown上限を確認。固定CollectorへSDK実OTLP送信したfixtureは7count、秒histogram7、sum0.21、0.025bucket0/0.05bucket7、source instance/version保持を確認。これらは実API/DBの受入を代替しない。

PE-012Bの実API/DB/log/runtime、AT-06の2Pod/rollout/restart、AT-08の5分PromQL窓/p95/無通信N/Aは配備後に検証する。未実施の結合項目を合格扱いにしない。外部の観測障害はCRUDへ同期依存を追加せず、PE-014で時刻付き障害試験を行う。

2026-10-03 SourceLab実結合: SDK版ff5af7e7305e36299b9b0b5629a0d9a016271619、固定image digest ec1ab2e08e26381b554d39c4c133b5ecf2b4056ddcec9d81d740d13a67318db7、manifest main6164760a27f510b29e419a2db8746f8651c9b666。frontend経由6要求201/200/200/409/400/404でHTTP6/DB4spanがNR account8572010 USに保存され、HTTP-logのspan IDとDB parent ID、Resource版/UUID/Pod UIDを照合した。synthetic fixture以外の既存データを変更していない。
実2プロセスの累積count各+10を確認。専用canary同Pod UID a1b578c0-f848-427a-b06a-67adedabcc4aでcontainer再起動0→1、UUID068ccdb5-e7ee-416b-b668-77c6e39ecef7からba62a768-0968-4b59-b03c-8c1979d8d34cへ変更しmain UUIDは不変。旧/新系列が衝突しないことを確認。
この文書版の配備切替を使って旧/新source SHAと実imageIDを再照合する。AT-08の5分実負荷は検証中、Collector障害/復旧はPE-014へ残す。未完了のATをClosedの根拠には使わない。証跡はoutput/core-platform-v1-phase1-phase2-20261002/evidenceのPE012-*に秘密値なしで保存。
