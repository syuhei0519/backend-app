# PE-002B migrationの有限待機と復旧

application-manifestのJobはdeadline 180秒、backoffLimit 1、Never、Sync wave -10、BeforeHookCreation/HookSucceeded。失敗Jobを残して診断し、backend Deployment wave 0を失敗hookの後に進めない。

backendのDB待機は最大10試行、各Pingのcontextは5秒以内、試行間は2秒。最後の失敗後に待ち続けず、接続errorを秘密値のあり得るログへ転記しない。migration全体のcontextは170秒で、Job上限まで10秒をcleanupに残す。DB待機・lock取得・SQL・履歴commitは同じcontextの期限を使う。親contextの中断にも従う。

session advisory lock 7742340081を使う。正常終了では5秒以内のunlock成功を確認してconnectionをpoolへ戻す。lock取得の中断やunlock失敗はsession状態が不確かなため、connectionをHijackして閉じる。SQLファイルごとのtransactionにDDLと履歴INSERTを含め、途中中断時のrollbackも独立した5秒contextで試みる。適用済versionはno-opとする。

通常のgo testは有限試行・短い親期限・復旧・キャンセルを検証する。実DBテスト `TestMigrationDisposableDatabase` は通常CIでSkipし、承認済み使い捨てclusterでだけ実行する。PE_ACCEPTANCE_TARGET=kind-core-platform-at0102と127.0.0.1:18083のport-forwardを必須とし、PE_MIGRATION_TEST_URLは資格情報をファイル/履歴/ログへ書かずprocess環境へ渡す。専用schemaの不存在を確認して作成し、テスト後そのschemaだけを削除する。稼働DBでは実行しない。

2026-10-02に隔離kindで実DBテストがPASS。適用済履歴1行と時刻の不変、advisory lock競合の期限終了と再実行、SQL途中中断で部分DDL/履歴が残らず再実行成功を確認した。値を除いた証跡は実装報告のPE-002B-migration-integration.txt。これは未統合の修正コードをGoで試験した結果であり、修正版imageのArgo配備受入ではない。

AT-03のJob deadline超過・失敗時Deployment停止・失敗ログ保存・同revisionのbackend Application全体明示sync・CRUDは、MR統合後のPhase 0由来確認済みimageで別途受入する。DB修復だけで失敗revisionの自動再同期が起きると仮定せず、selective syncを通常復旧へ使わない。

参照: [PE-002](https://gitlab.com/syuhei-platform-engineering-lab/platform-gitops/-/work_items/2)、[設計正本](https://drive.google.com/file/d/14nYKc_C7iEXrfmFSWD-wA2-FJzSWgfbe/view)。
