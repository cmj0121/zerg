package loader

import "embed"

// stdlibFS is the toolchain-controlled set of `std/*.zg` modules shipped
// inside the zerg binary. The loader resolves any user `import "std/<name>"`
// against this FS and rejects misses with a "stdlib module not found"
// diagnostic — there is no working-directory fall-through for `std/...`
// paths.
//
// Unit 2 stands the embed mechanism up; the actual std/io.zg, std/strings.zg,
// std/math.zg, std/os.zg sources land in Unit 3 / Unit 4. The placeholder
// file currently in stdlib/ exists so the //go:embed directive has at least
// one matching file (Go errors at build time otherwise).
//
//go:embed stdlib/*.zg
var stdlibFS embed.FS

// stdlibModulePath returns the embed-FS path for a stripped module name.
// Centralised so both the lookup and the diagnostic agree on the layout.
func stdlibModulePath(name string) string {
	return "stdlib/" + name + ".zg"
}
