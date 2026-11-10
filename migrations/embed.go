// Package migrations embeds the SQL migration files into the binary so the
// migration mode needs nothing but the executable and a database.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
