package configs

import "embed"

//go:embed *.yaml *.md prompts
var FS embed.FS
