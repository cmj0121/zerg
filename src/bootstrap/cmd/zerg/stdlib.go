package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/cmj/zerg/src/bootstrap/internal/loader"
)

// stdlibCmd prints the catalog of toolchain-supported stdlib modules with
// short descriptions. The set comes from loader.Catalog so the CLI surface
// and the loader's notion of "what the toolchain ships" stay in sync.
type stdlibCmd struct{}

func (c *stdlibCmd) Run() error {
	return printStdlib(os.Stdout)
}

// printStdlib writes one module per line with name and description aligned
// in two tab-padded columns. Factored out so the test can capture output
// without going through the CLI binary.
func printStdlib(out io.Writer) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, e := range loader.Catalog() {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", e.Path, e.Description); err != nil {
			return err
		}
	}
	return w.Flush()
}
