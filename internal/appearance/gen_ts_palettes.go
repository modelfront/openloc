//go:build ignore

// gen_ts_palettes.go writes the frontend's built-in palette constant from the Go
// definitions in palettes.go, which are the single source of truth
//
// Why generate instead of fetching: the frontend resolves a palette
// synchronously before React's first paint to avoid a colour flash, so the
// built-ins have to be a bundled constant. Generating keeps that property while
// leaving exactly one place to edit.
//
// The rendering itself lives in palettes_ts.go so it can be unit-tested for
// staleness; this file is only the file-writing shell.
//
// Usage:  go run gen_ts_palettes.go
// Output: ../../web/src/lib/builtin-palettes.generated.ts
//
// Wired into `make types`. Never edit the output by hand.
package main

import (
	"fmt"
	"os"

	"openloc/internal/appearance"
)

const outPath = "../../web/src/lib/builtin-palettes.generated.ts"

func main() {
	if err := os.WriteFile(outPath, []byte(appearance.RenderTypeScript()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d palettes, %d anchors)\n",
		outPath, len(appearance.BuiltinPalettes), len(appearance.AnchorKeys))
}
