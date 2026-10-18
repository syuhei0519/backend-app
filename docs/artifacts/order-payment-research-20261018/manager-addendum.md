{
  "representation": "edited_derivative",
  "public_issue": "https://github.com/syuhei0519/backend-app/issues/50",
  "source_updated_at": "2026-10-18T04:07:39Z",
  "issue_title": "[Order→Payment] 1経路の同期呼び出しと再送・タイムアウトを実装する",
  "consumer_scope": "syuhei0519/backend-app#50 implementation only",
  "current_manager_constraints": [
    "Backend-only implementation can proceed after Manager dispatch authorization. Shared manifest values/schema/validate.py/README are serialized: platform-gitops#83 manifest owner first, then Issue50 uses its accepted fixed base. No concurrent shared-file edits.",
    "Publication permission and limited Consumer use permission are distinct. No main/product-code/CI/PR change, deployment or cluster permission is granted by this publication.",
    "All product AC remain unverified. Current independent Test/Review, CI, required GitHub conditions and aggregate integration gates remain required."
  ],
  "acceptance_criteria": [
    {
      "id": "SC1",
      "verbatim": "OrderからService名でPaymentを呼び出せる。",
      "source": "https://github.com/syuhei0519/backend-app/issues/50"
    },
    {
      "id": "SC2",
      "verbatim": "模擬決済の成功・拒否が呼び出し元へ正しく返る。",
      "source": "https://github.com/syuhei0519/backend-app/issues/50"
    },
    {
      "id": "SC3",
      "verbatim": "Paymentが応答しない場合、決めた時間でタイムアウトし成功扱いしない。",
      "source": "https://github.com/syuhei0519/backend-app/issues/50"
    },
    {
      "id": "SC4",
      "verbatim": "同じ要求を再送しても決済記録が重複しない。",
      "source": "https://github.com/syuhei0519/backend-app/issues/50"
    },
    {
      "id": "SC5",
      "verbatim": "Cloudで契約・結合テスト、kindで実際のService経由の通信を1回確認する。",
      "source": "https://github.com/syuhei0519/backend-app/issues/50"
    },
    {
      "id": "SC6",
      "verbatim": "API仕様と確認手順がGitに残る。",
      "source": "https://github.com/syuhei0519/backend-app/issues/50"
    }
  ]
}
