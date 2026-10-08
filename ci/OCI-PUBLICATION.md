# PE018B 検査済みOCIの公開準備

既定無効の `OCI_DELIVERY_VALIDATION_ENABLED` は保護mainの明示APIでだけpublish-ociを選択する。正常scan/SBOM/不変Package、完了build/scan/writer/互換性/runtime、現在保護mainと実行中publisherを実APIで照合し、同layoutをcrane pushする。SHAタグ存在/照合不明/失敗は上書きせず拒否し、remote digest/OCI注釈を再照合する。

既存通常配信は維持する。今回の中間実装は実新規公開の受入前であり、再取得検査・manifest consumer・rollback・旧経路撤去・PE018B完了を主張しない。過去の成功や資格設定は再実行しない。
