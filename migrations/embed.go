// Package migrations exposes the SQL migration files as an embedded filesystem
// so the binary is self-contained (no files to ship alongside it). The .sql
// files here remain the source of truth for the data model.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
