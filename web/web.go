package web

import "embed"

//go:embed static
var staticFS embed.FS

func StaticFS() embed.FS { return staticFS }
