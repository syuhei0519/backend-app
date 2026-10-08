# backend-app

Go標準HTTPサーバーとpgxによるAccount API。`server migrate`でforward-only migrationを適用します。

Go・HTTP/DB処理・SQL migration・イメージ・CIは本repository、migration実行Job・account固有DB/PVC・接続Secret参照はapplication-manifest、Argo Application/AppProject/Namespaceはplatform-gitopsが所有します。DBのSecret更新だけではDB roleの認証情報は変わりません。rotationの実行は利用者承認が必要です。

固定版台帳の正本はplatform-gitopsの `bootstrap/versions.lock.yaml` です。`ci/tool-versions.env` はPE-001時点で未作成であり、現在の実行値は `.gitlab-ci.yml` / Dockerfileにあります。後続PEで追加する際は台帳との対応を照合します。`frontend` / `backend` / `application-gitops` は別系統で、今回の実装対象ではありません。[PE-001基準記録](https://gitlab.com/syuhei-platform-engineering-lab/platform-gitops/-/blob/main/docs/implementation/PE-001-baseline.md) と [PE-001 Issue](https://gitlab.com/syuhei-platform-engineering-lab/platform-gitops/-/work_items/1) を参照してください。
