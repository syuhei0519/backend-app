// 移行SQLをバイナリに埋め込む入口。internal/store/postgres/migrate.goがFilesから読む。
// 下のgo:embedはコンパイラへの指示なので翻訳・移動しない。Docker内の外部SQLだけを変えても埋込SQLは変わらない。
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
