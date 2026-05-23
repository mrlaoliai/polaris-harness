package builtin

import "embed"

//go:embed */impl.wasm
var FS embed.FS
