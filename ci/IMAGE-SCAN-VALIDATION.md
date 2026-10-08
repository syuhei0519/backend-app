# PE017B 検証専用scan/SBOM/不変record

PE016Bの実受入と親16 Closed後、PE017F受入済み固定source d5226afの共通ライブラリ・scan/finalizer/rescanをbackendへ移植する。OCI/IMAGE_SCAN/RELEASE_RECORD_STORE/AT17_RESCANのflagは既定false。既存main publish→verify→proposalをPE018Bの完全切替まで維持する。

同一OCI tarのsource/project/元build/checksum/digestとcrane readerを照合し、実Trivy DBの鮮度と全finding方針を適用する。同jobの同reportをCycloneDX1.7へ変換し、固定embedded schema・全package inventory・config/source・record/scan/SBOM bytesを結合する。scan/SBOM/外側予算のいずれかが失敗した場合はfailed recordを保存し、SBOM/input成功proofを除去する。成功を過去runや別入力で補わない。

writerは保護main・release-evidence環境・実APIのcurrent main/job/pipeline/元buildを確認し、CI_JOB_TOKENによる単一writerでscan→SBOM→record-lastを不変Generic URLへ保存する。メタデータ参照用read_api tokenを保護・hidden・masked・release-evidence scopeで別に用意する必要がある。未保護branchにwriterは選択されない。公開artifactは型付き保存proofのみで、生scanner内容・資格値は含めない。

AT04の第二manual scanは実DBが第一より新しい時だけ同元OCIで行い、意図したSBOM生成停止により第二failedを独立URLへ保存する。元artifact期限内に実DB更新を確認する。DB日時をfixtureで書き換えず、旧第一の合格で第二を合格にしない。

固定実装source `0b855190a3fc52978903a7b2b851d577078856c9` の[実native CI 2910986322](https://gitlab.com/syuhei-platform-engineering-lab/backend-app/-/pipelines/2910986322)は全10ジョブ成功。元build `16922319795` とscan `16922319798` を結合し、同OCIのTrivy検査・CycloneDX 1.7の全38components/36packages・全schema/依存graph・config/source/実policy bytes/実DB/固定run URLを受入した。layout/crane/Trivy互換、network noneの隔離実行UID10001/live200/DB不在ready503、4ジョブの容量・時間予算も実合格。buildのrootless store測定最大10,325,848KiBは10GiB上限内で、連続ピークやメモリの計測とは主張しない。

SBOM inventoryはscannerのEpoch・Version・Releaseを含む完全なバージョンと照合する。自作Go mainmoduleの未知versionは引き続き拒否するため、Dockerで実sourceSHA由来の開発metadata `0.0.0+git.${VCS_REF}` をコンパイルし、trimpathとTrivyが検出できるGo symbolsを保持する。既存OTel service.versionはsourceSHAを維持する。安定版v1リリースを名乗る変更ではない。失敗時の診断は固定分類のみで、raw reportやschema errorを公開しない。

同sourceのSBOM生成失敗[2911011402](https://gitlab.com/syuhei-platform-engineering-lab/backend-app/-/pipelines/2911011402)と解析失敗[2911011638](https://gitlab.com/syuhei-platform-engineering-lab/backend-app/-/pipelines/2911011638)を各一度実行し、両ケースを実受入した。検査ジョブだけが意図どおり失敗し、他9ジョブは成功。独立した実failed record、SBOM null、成功用input/SBOM proof/SBOM artifactの404を確認した。過去成功runへのfallbackは行わない。

保護writer、異なる実DB二run、両run実reader/PUT403、保持・入替・中間main AT05は未受入。この説明書や正常nativeの部分受入を親17完了へ置換しない。PE018Bのregistry公開/再取得/全面切替は別工程である。
