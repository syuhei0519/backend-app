-- 初回schema移行。internal/store/postgres/migrate.goがtransaction内で実行し、version=1を履歴へ記録する。
-- 既存適用済み環境ではこのファイルを再実行しない。将来の変更は別versionのSQLを追加する運用。
CREATE TABLE accounts (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name VARCHAR(100) NOT NULL CHECK (length(trim(name)) > 0),
  email VARCHAR(254) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- email小文字の一意性をDBでも保証する。並行作成の競合はStoreが23505→ErrConflict→HTTP409へ変換する。
CREATE UNIQUE INDEX accounts_email_unique_idx ON accounts(lower(email));
