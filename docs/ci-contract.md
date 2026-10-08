# PE-008 CI実行契約（Phase 0）

PE-001統合済みmainからの変更。Phase 2のscan/SBOM/OCI切替はここで実装しない。既存のmain公開→remote検証→manifest提案を維持する。

| pipeline | 検証 | 公開/検証/提案 | 条件 |
|---|---|---|---|
| 通常branch、MRなし | 全validateジョブ | なし | branchあり |
| 同project MR | 全validateジョブ | なし | source project == pipeline project |
| branch、MRあり | push pipeline抑止 | なし | 重複抑止 |
| 保護main | 全validateジョブ | 全deliveryジョブ | protected == true |
| 保護されていないmain | 全validateジョブ | なし | 公開を拒否 |
| tag | pipelineなし | なし | 最初のworkflow規則 |
| fork/外部MR | pipelineなし | なし | source project不一致 |

| job | needs | cache | artifacts期限 |
|---|---|---|---|
| 言語固有lint/typecheck/test/build、Go製manifest-helper-test | なし（validate stage） | lock/input hash + tool + linux/amd64 + protected境界 | buildのみ1日 |
| publish-image | 全validateジョブ | Registry cache入出力無効 | image.env/metadata 30日 |
| verify-image | publish-image（artifact取得） | なし | image.env 30日 |
| propose-manifest-update | verify-image（artifact取得） | なし | なし |

失敗はallow_failure=false（既定）。手動再実行でもpublishの全needsを満たす。retryはRunner障害/外部依存障害/Runner中断/ジョブのstuckに最大1回。script_failure（静的検査/テスト/ビルド/方針違反/スクリプト内通信失敗）を自動retryしない。deliveryは直列resource_groupを維持する。

cacheはダウンロード依存とコンパイルcacheだけ。node_modules/dist/配信imageをcacheで信頼しない。keyは依存入力hash、固定tool、対象arch、CI_COMMIT_REF_PROTECTED。unprotect=false。GitLabの保護cache分離設定と実書込権限の確認はAT-13に別記する。

rulesは認可ではない。同project MRも信頼済みコードだけを例外付きRunnerで実行する。MRからRegistry/package/cacheへの技術的書込可否はrulesでは証明できない。Protected/masked変数は保護mainと用途別environmentに限定し、MRの変更済みコードへ秘密を渡さない。トークン発行/scope/保護設定はこのMRで変更していない。

tool-versions.envは実行imageと一致させる。正本台帳bootstrap/versions.lock.yamlとの版/digest/対応commit照合はplatform側MRで記録する。

検証: ローカル言語テストとMR pipelineを記録。branch/main/tag条件の実pipeline確認、失敗pipeline後続停止、AT-13の使い捨てSHA/cache/package実権限測定は未実施。合格とは扱わない。マージ後にmain配信を確認し、提案gate結合はPE-004で受入する。

rollback: この変更commitをrevertする。Registry cache再有効化は明示レビューを要する。

参照: [GitLab YAML](https://docs.gitlab.com/ci/yaml/)、[cache](https://docs.gitlab.com/ci/caching/examples/)、[PE-008](https://gitlab.com/syuhei-platform-engineering-lab/platform-gitops/-/work_items/8)。

## 2026-10-02 AT-13の実権限

未保護の信頼済み試験branchでjob 16885612284が完全40桁SHAタグ、使い捨てregistry main-cacheタグ、証跡packageへ実書込し、いずれもHTTP201だった。提案credentialは未保護jobに渡らない。ci_separated_caches=true、cacheのunprotect=falseでRunner cacheの保護区分を分ける。registry cacheの入出力は無効を維持する。使い捨てregistry cacheタグへのwrite許可と、Runnerの保護cache分離を混同しない。

CI_JOB_TOKENによる同project registry/packageへの書込は技術的に拒否されていない。rulesを認可境界と扱わず、同projectも信頼済みコードのみ、外部MR禁止、変更CIの事前確認を有効化条件とする。registry/package保護設定を強化した保証はまだない。Phase 2の証跡不変性はPE-017/018で別受入する。

OwnerによるAPI pipelineの変数AT13_FORCE_VALIDATION_FAILURE=trueはhelper検証をscript_failureで終了する受入fixture。通常はfalseで言語テスト後のdeliveryを維持する。全deliveryが失敗needsで停止し、script_failureがretryされないことを実mainで測定する。pipeline変数overrideの最小権限はOwner。秘密値はfixtureに渡さず、証跡はHTTP結果・SHA/job IDだけを記録する。