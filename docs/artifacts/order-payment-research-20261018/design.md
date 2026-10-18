{
  "representation": "edited_derivative",
  "context": "Historical Research/Design snapshot; later publication is separately authorized. Original prohibitions/readiness observations describe the Research stage and are not a statement that Issue50 is absent now. No product implementation acceptance is claimed.",
  "design": {
    "title": "担当② Order→Payment 最小通信設計",
    "stage": "Research/Design only; no implementation, GitHub writes, API billing, Secret/permission changes or cluster actions performed",
    "decision": {
      "route": "POST /api/orders/{id}/payments",
      "purpose": "既存Orderに対する独立した模擬決済要求。Order作成APIへ埋め込まず、Order業務状態を増やさない。",
      "request": {
        "required": [
          "request_id",
          "amount_minor",
          "currency",
          "simulate_result"
        ],
        "example": {
          "request_id": "550e8400-e29b-41d4-a716-446655440000",
          "amount_minor": 1000,
          "currency": "JPY",
          "simulate_result": "success"
        },
        "validation": "path idは既存の正のint64字句。bodyは既存Paymentの4入力制約を継承。request_idは非nil canonical lowercase UUID、amount_minorは正のint64、currency=JPY、simulate_result=success|decline。order_idはpathから生成し、bodyで与えると400。既存strict JSONと同じ16KiB/UTF-8/必須・未知・重複・null・余剰拒否、400/413/415/405。"
      },
      "flow": [
        "入力を検証する。新routeの処理予算をbody検証後から5秒とする。",
        "order.Service.Getで既存Orderを読む。404/503/500は既存Orderのsanitize済み応答。未存在・失敗時はPaymentを呼ばない。",
        "path idをorder_idにし、残り4入力を変更せず既存POST /api/paymentsへ送る。DB transactionやrow lockを跨いでHTTP待機しない。",
        "有効なterminal Paymentを検証して呼出し元へ返す。Order DB更新・新table/migration・Account/Notification呼出しは加えない。"
      ],
      "response": "Paymentの検証済みresource全フィールド（入力5個+id/status/created_at）を返す。新規=201、同一再送=200。simulate_result=successはstatus=succeeded、declineはstatus=declined。declinedをsucceededに変えない。拒否の201/200は模擬要求の記録成功であり決済成功ではない。Locationは転送しない（Order側GETは追加しない）。",
      "timeout": "Payment DNS解決・connect・request・response headers/body取得/検証まで一つのcontext.WithTimeout(r.Context(),2*time.Second)で上限2秒。親の残予算が短ければその期限。新route全体は検証後5秒（Order DB既存上限3秒を含む）。既存server WriteTimeout 10秒を維持。CPU停止等のOSスケジューリング遅延を厳密なwall-clock保証とはしない。ReadTimeoutは既存10秒を維持。",
      "errors": [
        {
          "condition": "Payment未設定",
          "http": 503,
          "code": "payment_unconfigured"
        },
        {
          "condition": "下流deadline超過",
          "http": 504,
          "code": "payment_timeout"
        },
        {
          "condition": "DNS/connect/reset等の非timeout輸送失敗、Payment 503",
          "http": 503,
          "code": "payment_unavailable"
        },
        {
          "condition": "Payment 409 idempotency_conflict",
          "http": 409,
          "code": "idempotency_conflict"
        },
        {
          "condition": "予期しないHTTP（3xx/4xx/5xx）、不正/過大/不一致body、非terminal/欠落field",
          "http": 502,
          "code": "payment_bad_response"
        }
      ],
      "error_envelope": "既存error.code/error.message形。messageは固定のHTTP status text。raw downstream body/URL/SQL/DSN/headersは返さない。400等こちらで予防した入力をPaymentが拒否した場合は契約不整合502。Payment 409/503のenvelopeが不正なら502。parent cancellationで成功を捏造せず下流contextをcancelする。",
      "replay": "Paymentの既存UNIQUE(request_id)、ON CONFLICT +別SELECT、decoded input比較を再利用する。新規・逐次/並行再送・Order API再起動後も同じrequest_idと5入力は同じid/status/created_at、row count=1。同じrequest_idで別Order/amount/currency/simulate_resultは有効入力なら409（無効入力は400が先）。別request_idは別要求であり同じOrderへの複数試行を許す。TTL/Idempotency-Key/Order側idempotency tableを追加しない。",
      "ambiguous_result": "504/503は『未決済』の断定ではなく、commit済みで応答を失った可能性を含む結果不明。自動再送しない。呼出し元が同じrequest_id・同じ入力を再送し200または201のterminal結果を得る。新request_idへ置き換えると別支払記録になる。",
      "http_client": "専用net/http clientをprocess起動時に注入。固定設定URLのみ使用、Proxy=nil、redirect禁止、POSTを1回だけ送る。Idempotency-Key/X-Idempotency-Keyを付けず、独自retry middlewareを使わない。GetBodyによるPOSTの暗黙再送を避ける構成にし、wire request回数をテストする。responseは16KiB+1で上限判定しbodyをClose。required fields・id>0・時刻・入力一致・terminal statusを検証し、未知の追加fieldはサイズ制限内で許すがduplicate/trailing JSONは拒否する。",
      "configuration": "ORDER_PAYMENT_URLだけを追加。空=新endpointは503 payment_unconfigured、既存Order作成/取得/readyzは従来動作。設定時はhttp://host:portのoriginのみ（userinfo/path/query/fragmentは禁止、host/port検証、値はログへ出さない）。学習kind値=http://learning-payment:8080（Orderと同namespace）。Cloud testsは隔離loopback listener。timeoutはコード内2秒定数として固定し任意env surfaceを増やさない。Payment非応答をOrder readiness failureへ波及させない。"
    },
    "alternatives": [
      {
        "option": "採用: 既存Orderに専用payment endpoint、既存PaymentへHTTP、追加永続状態なし",
        "reason": "現存API・DBのidempotencyを再利用し、Order→Payment 1経路のACを直接証明できる"
      },
      {
        "option": "Order作成時に同期Paymentと支払済み状態追加",
        "reason": "金額の無いOrder schemaを拡張し、二重書込/補償問題を作るため不採用"
      },
      {
        "option": "Paymentを汎用proxy、Order存在確認なし",
        "reason": "最小差分は小さいがOrder経由の有効な要求境界が曖昧になるため、既存Getで存在確認する案を採用"
      },
      {
        "option": "Kafka/Saga/outbox/auto retry/mesh timeoutを先に導入",
        "reason": "明示対象外。Service DNSはIstioなしで検証する"
      }
    ],
    "changes": {
      "backend_owner": "Backend implementation担当。外向きHTTP契約、入力/応答検証、timeout/error/replay、注入とtests、API仕様を所有。",
      "backend_modify": [
        "cmd/order-server/main.go",
        "internal/orderconfig/config.go",
        "internal/orderconfig/config_test.go",
        "internal/orderhttp/server.go",
        "internal/orderhttp/server_test.go",
        "cmd/order-server/main_test.go",
        "docs/order-openapi.yaml",
        "docs/order-api.md"
      ],
      "backend_add": [
        "internal/orderpayment/service.go",
        "internal/orderpayment/service_test.go",
        "internal/paymentclient/client.go",
        "internal/paymentclient/client_test.go",
        "tests/orderpaymentintegration/orderpayment_test.go",
        "tests/orderpaymentintegration/disposable.go",
        "docs/order-payment-communication.md"
      ],
      "implementation_seam": "orderpayment.ServiceはOrder GetterとPayment Creator interfaceだけに依存。orderhttpの既存Newを保つかoptional dependency constructorを追加し、既存tests/entrypoint互換を維持。Payment側HTTP/SQLは変更しない。既存cmd/order-serverのmigrate動作がPayment未設定でも実行可能であることを維持。",
      "manifest_owner": "application-manifest担当。既存learning/chartへ非秘密のOrder専用接続値を追加。一般chartの変更時もaccount/payment/notification renderが変わらないことだけ静的確認する。",
      "manifest_modify": [
        "learning/chart/values.yaml",
        "learning/chart/values.schema.json",
        "learning/chart/templates/api.yaml",
        "learning/chart/templates/validate.yaml",
        "learning/validate.py",
        "learning/README.md"
      ],
      "manifest_detail": "orderPaymentURL: ''を追加し、service=orderかつ非空時だけAPI templateのenvへORDER_PAYMENT_URLを注入。order以外で非空はschema/templateで拒否。helpers learning.envはmigrationにも共用されるため変更せず、migration envへURLを加えない。validate fixtureはOrder on/offと他3service unchanged/非Order設定拒否を確認。使用sourceCommits.orderは新採用commitを非秘密の実行valuesで上書きし、他service pinを一括変更しない。",
      "manifest_add": [
        "learning/communication-order-payment.values.example.yaml"
      ],
      "example_values": "service: order / orderPaymentURL: http://learning-payment:8080 のみのoverride例。imageID/source/owner/namespaceは承認された別入力と合成してCloud描画。Secrets/実context/実owner/実imageIDをsampleへ入れない。",
      "no_changes": [
        "Account/Notification API・DB・Dockerfile",
        "Payment API/DB/migration",
        "Order DB/migration/business state",
        "charts/order/*（無効な正式release経路）",
        "platform-gitopsのcharts/root・charts/workloads・Argo登録",
        "Istio/NetworkPolicy/監視/trace/dashboard/alert",
        "CI/release hardening基盤の再設計"
      ],
      "platform_owner": "監視はplatform担当。Istio準備も後続platform担当。通信ACにmeshを必須化しない。既存NetworkPolicy等で遮断されていた場合だけplatformへ必要なOrder→Payment TCP/8080とDNS解決の最小許可設計を返し、本taskで権限やpolicyを変更しない。"
    },
    "issue_recommendation": {
      "decision": "新規小Execution Issue案を1件推奨。今回は作成しない。",
      "title": "[Order→Payment] 模擬決済の同期呼出し・2秒timeout・同一要求再送を実装する",
      "repository": "syuhei0519/backend-app",
      "scope": "1主変更=既存Orderに対するPayment呼出し1経路。主Validation Layer=Cloudの2 API/2 DB結合fixture。manifest設定とkind smokeは同じ契約を渡す別担当subcheckとして追跡。",
      "acceptance_source": "今回の委任AC全6文を原文どおり採用し、既存Account IssueのACと混ぜない。",
      "existing_fit": [
        {
          "issue": "backend-app#7",
          "fit": "不適合。現行最新Scopeと3ACはOrder→Account。今回で書き換えたり完了扱いにしない。"
        },
        {
          "issue": "backend-app#13/#14",
          "fit": "参照のみ。fixture/contractの考え方は利用できるが現行Account対象の拡張をこのTaskに混ぜず、親Closeを前提にしない。"
        },
        {
          "issue": "backend-app#39",
          "fit": "完了済みPayment API/永続request_idを能力として再利用。独立成功履歴を今回の連携合格としない。"
        },
        {
          "issue": "backend-app#2 / application-manifest#21",
          "fit": "2026-10-18に学習独立稼働がclosed。固定learning/chart/Service名/build/load手順を再利用。正式GHCR/Argoへscope拡張しない。"
        },
        {
          "issue": "backend-app#45/#46/#24/#29",
          "fit": "Saga/非同期復旧の別責務。今回の前提にしない。"
        },
        {
          "issue": "platform-gitops#8",
          "fit": "Istio後続。今回のService DNS通信の依存にしない。"
        }
      ],
      "dependencies": "hard capability: 採用backend pinの既存Order/Payment APIと所有DB、kind受入では既存の承認済みlearning Service/runtimeと新Order image/config pin。依存IssueのCloseや正式release完了ではなく能力/固定契約を照合する。",
      "history": "既存issue/PR/失敗/attempt/retry/approval履歴を維持。新Issue名は既存論理工程の予算リセットを意味しない。Managerが新scope/旧scopeとの関係と最新台帳をdispatch前に確定する。"
    },
    "validation": {
      "current_result": "設計とソース読取りのみ。コード/API/DB/cluster/CIテストは未実行。全製品ACはunverified。",
      "cloud_contract": "Go標準httptestでPayment consumer→既存paymenthttp provider契約を実行。201 succeeded/declined、200 identical replay、409 changed input、503、2秒応答なし/slow body、connect failure、redirect/malformed/oversize/missing/mismatched result、親cancel、wire POST回数1、未知field追加許容、必須field削除拒否を最小tableで確認。timeoutテストは下流がctx取消で終了するか解放するfixtureにする。",
      "cloud_integration": "PlatformEngineering Cloudで隔離使い捨てOrder/Payment PostgreSQL（正しいorders/order_app、payments/payment_app）、既存migrationsと2つの実API processを起動。Docker等の利用可能な承認済みrunnerで十分でkind不要。既存paymenttestはKubernetes専用のためそのままCloudへ流用しない。ordertestの使い捨てDBパターンを基に最小2DB fixtureだけ用意する。外部DSN/本番DB/既存Lab volumeは使用しない。新Secret作成・権限変更が必要なrunnerは勝手に作らず環境阻害として返す。 既存ordertest.Startはorders/accounts限定でpaymentsを受け付けないため、Account用helperの許可対象を広げず専用orderpaymentintegration fixtureでpayments/payment_appを扱う。新規test-owned DBの初期化も後続の明示許可範囲で行い、今回の権限変更禁止から実行許可を推論しない。",
      "integration_cases": [
        "既存Orderを1件だけ作り、success/decline各request_idの201とterminal status/全fieldを確認。",
        "同じrequest_idの逐次/同時再送で同一id/status/created_at、Payment row count=1。変更入力は409。Order API再起動後の同一再送でもrow count=1。",
        "Order未存在/不正入力はPayment wire call 0かつ行0。",
        "Paymentがcommitした結果の応答だけをfixture proxyで落とす。Orderは504/503で成功扱いせず、同じIDの手動再送で既存rowを回収しrow count=1。",
        "headers/body無応答stubで2秒deadlineを実測。既定T=2sを下回らない遅延fixture、テスト上限T+1s、route全体上限5s+1s（通常Order read）を記録。実ネットワーク停止時の即時503と無応答504を混同しない。",
        "既存Order create/getとmigrateの最小互換確認。Account/NotificationにはHTTP/DB mutationをしない。"
      ],
      "commands_proposed": [
        "go test -race ./internal/orderpayment ./internal/paymentclient ./internal/orderhttp ./internal/orderconfig ./cmd/order-server",
        "go vet ./internal/orderpayment ./internal/paymentclient ./internal/orderhttp ./internal/orderconfig ./cmd/order-server ./tests/orderpaymentintegration",
        "ORDER_PAYMENT_DISPOSABLE_TEST=1 go test -count=1 -run '^TestOrderPaymentLifecycle$' -v ./tests/orderpaymentintegration",
        "python3 learning/validate.py"
      ],
      "kind_smoke": "実KubernetesのみManagerが別途起動するローカルkind Test。既存承認済みcontext/cluster/namespace/ownerと保持資源UIDをread-only照合し、Secretには触れない。Cloudで固定buildした新Order archive/config IDと固定render/valuesを照合し、承認済み更新範囲のOrder Deploymentだけ適用。Payment Service名=learning-payment:8080、Order envとselector/ready EndpointSlice、両PodのimageID/source/configを記録。localhost forwardは入口をOrder Serviceだけへ向け、POST /api/orders/{id}/paymentsの成功・拒否と同一ID replayを1回の実行セッションで観測する。Paymentへの呼出し先はPodIP/localhost/port-forwardではなくService DNSであることをenv/固定renderで示す。Payment SQL重複・timeout細目はCloudで検証し、kindで全suiteを反復しない。 Service DNS短名が同namespaceのServiceへ解決されることを実観測するため、既存承認済みOrder Pod内の利用可能な診断手段か承認済み診断Podから名前解決を記録する。診断手段がなければ勝手な新image/tool導入をせず、Service名への実HTTP成功とService/EndpointSlice設定の対応を記録し制約を明記する。",
      "kind_cleanup": "本taskでは実行しない。後続は更新前Order image/config/replicas/UIDをprivate inventoryに保存し、失敗時はその同じOrderの既存受入configへ戻す。Payment/DB/PVC/Secret/Account/Notificationを停止・削除しない。port-forward等自作processのみ終了。既存保持namespaceを新規所有と見なさず、namespace/cluster/volume削除は禁止。復旧/cleanupに追加権限が必要なら停止してManagerへ返す。",
      "evidence": "Cloudは実code/config SHA、DB/runtime identity、コマンド、件数/実時間/結果、cleanupを固定test-reportへ。kindは実context/cluster/namespace/UID、image対応、Service DNS、HTTP結果とrollback/cleanupを非公開原本へ。Secret・kubeconfig・実環境識別子をpublic Gitへ出さない。公開するなら安全確認済み派生版に別承認を要し、原本と同一bytesとしない。"
    },
    "readiness": {
      "implementation": "技術設計は確定し、Backend実装はkind/Istio/監視を待たず開始可能。ただし今回の許可はResearchのみなので現時点のImplementation Readyはblocked（実装許可・Managerのscope/台帳/対象pin確定待ち）。UserのAPI/DB選択待ちはない。",
      "deployment": "blocked。新Order image/configの固定配送、ローカルkind対象/既存inventory/復旧pin/更新許可/必要既存credential利用範囲はこのResearchで未確認。前回独立稼働のclosedだけで新imageの配備を許可しない。",
      "merge": "blocked/未評価。PRなし、独立Test/Reviewとcurrent-head CI/保護条件未取得。実装者自己検証や設計Handoffを独立Testにしない。",
      "verification_phase": "全6ACとも最早premergeで実証可能。kind環境未準備でもpostdeployへ延期しない。後続の新Order imageを隔離kindへ一度配備して候補revisionを確認できる。",
      "integration_scope": "backend+manifestの複数repoを想定しaggregate_required。各PR readinessと全AC/必要merge集合・最新統合結果を別評価。最後のAC『Gitに残る』とIssue達成はManagerが承認された反映・統合後に確認する。",
      "deployment_effect": "新Issueのbackend build/test差分とmanual learning chartのみに限定する計画。恒久Order GitOps登録はdisabled。実PRごとのCI/Argoが影響するので現段階でnone認定はしない（unknownとしてlive確認待ち）。",
      "permissions": {
        "research": "既存認可されたGit読取り・設計成果物のprivate Library保存のみ（本依頼）。",
        "implementation": "後続Manager割当のbackend-app/manifest限定file編集、Cloudの使い捨てprocess/DB・image build/成果物保存。GitHub branch/push/PRは別の明示scopeに従う。",
        "kind": "既存kindへのread、承認されたOrder Deployment/configのみの更新、試験用Order/Payment行、localhost Order Service port-forward。cluster-wide admin、RBAC、NetworkPolicy、Secret値/設定、PVC操作は不要。新規資格情報/課金APIは不要。既存credential利用自体もManagerの承認済み経路で行う。"
      }
    },
    "remaining": [
      "実装・Issue作成・PR・cluster操作は今回禁止で未実施。",
      "Managerが新Issue/assignment名と全6ACのordinal IDを採用し、既存履歴・最新残予算と結び付ける。",
      "後続Cloud fixtureのDB runner能力、ローカルkindの正確な対象とOrderだけ更新する承認は起動入力へ追加する。",
      "実model/effort/tier/usageは観測不可。requestedSolHighを実行済みモデルの証明としない。"
    ]
  }
}
