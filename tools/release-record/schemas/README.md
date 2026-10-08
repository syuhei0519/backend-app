# 固定CycloneDX JSON schema

公式 [CycloneDX specification 1.7](https://github.com/CycloneDX/specification/tree/1.7/schema) の以下のファイルをbyteそのまま保存した。Apache-2.0 LICENSEを同梱する。

| File | SHA256 |
|---|---|
| bom-1.7.schema.json | df472ef4aaf593904c479293723a1a5c191d6672715c93b3c0b5c318f3914221 |
| spdx.schema.json | 54a6288292bc6c90b0d3952f5f939f17436fa76704ffe68a46e5b78539c7cc1b |
| jsf-0.82.schema.json | 8bae002c25e723db7ee1f26afde680ae1a2b1a8f6b4b4b0fd65dc3becb090aae |
| cryptography-defs.schema.json | 018ea7f78b5208ec647cfd10f669cc9c26aba6aceb79c4da7f9c0ef4c99b60de |

validatorはembedded byteのhashを照合し、固定schemaをfull compile、format assertionを有効化する。instanceの `$schema` や任意remote/file referenceからschemaを選択せず、loaderは拒否する。未知のSBOM版は採用しない。

実装固定版はjsonschema/v6 v6.0.2（go.sumで依存固定）。schemaだけでは同一性・完全性を証明できないため、追加でconfig digest・Trivy版・timestamp・component ref・依存参照・同一イメージ報告の全package Name/Versionの包含を検証する。packageの名前と版の照合はPURL/architecture全項目の一致を主張しない。run/source/recordの照合と実CI受入は別途必要。
