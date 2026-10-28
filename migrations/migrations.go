// Package migrations embeds the SQL migrations into the binary, so a deploy
// is a single artifact and the schema can never drift from the code.
package migrations

import "embed"

// FS holds every migration file, named NNNNN_description.sql.
//
//go:embed *.sql
var FS embed.FS
