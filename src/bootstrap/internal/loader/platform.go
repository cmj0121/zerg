// Platform-suffix file resolution for v0.13.
//
// A .zg file whose basename ends with one of the recognized platform suffixes
// (`_macos.zg`, `_linux.zg`) is compiled only when the host platform matches.
// On a mismatched host the file is treated as nonexistent for loader
// resolution.
//
// Mapping: runtime.GOOS == "darwin" → "macos"; runtime.GOOS == "linux" →
// "linux". On any other host neither suffix matches, so every _<known>.zg
// file is invisible — a deliberate v0.13 ship gate: only the two platforms
// we've designed for participate.
//
// Sibling-import lookup chain (see loader.go resolveImports): for
// `import "x"` from dir/a.zg, lookup order is dir/x_<host>.zg then dir/x.zg.
// Both existing on a matching host → ambiguity error. The wrong-host suffix
// alone produces the standard not-found diagnostic (the wrong-platform
// diagnostic is reserved for entry-file mismatch).
//
// CRITICAL DESIGN PIN: the platform-suffix rule is sibling-only. loadStdlib
// does NOT consult this table. If a stdlib module ever needs platform
// branching, it does so internally; that work defers to v0.14+.
package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// platformSuffixes is the closed set of host strings recognized by the
// platform-suffix rule at v0.13. New entries here must also extend
// hostPlatform()'s switch on runtime.GOOS.
var platformSuffixes = []string{"macos", "linux"}

// hostPlatformOverride is the test seam for hostPlatform(). Empty in
// production; set via SetHostPlatformForTest to simulate a chosen host
// without rebuilding the binary against a different GOOS.
//
// The sentinel value "none" simulates an unsupported host (no suffix
// routing); any other non-empty value is used verbatim as the platform
// string returned by hostPlatform().
var hostPlatformOverride string

// hostPlatform returns the platform-suffix string for the current host:
// "macos" on darwin, "linux" on linux, "" on hosts not in platformSuffixes.
// An empty return disables suffix routing — sibling resolution never probes
// any _<known>.zg file, and an entry file with such a suffix rejects under
// the wrong-platform rule because its suffix won't match "".
func hostPlatform() string {
	if hostPlatformOverride != "" {
		if hostPlatformOverride == "none" {
			return ""
		}
		return hostPlatformOverride
	}
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	}
	return ""
}

// hostName returns a user-facing host label for diagnostics. Matches
// hostPlatform() when nonzero; falls back to runtime.GOOS on unsupported
// hosts so the wrong-platform message still names the host the user is on.
func hostName() string {
	if h := hostPlatform(); h != "" {
		return h
	}
	if hostPlatformOverride == "none" {
		return runtime.GOOS
	}
	return runtime.GOOS
}

// SetHostPlatformForTest overrides hostPlatform() for the duration of the
// returned restore func. Use the sentinel "none" to simulate an unsupported
// host; use "" via the restore func to clear. Tests should always defer the
// restore so a panic doesn't leak the override to sibling tests.
func SetHostPlatformForTest(s string) func() {
	prev := hostPlatformOverride
	hostPlatformOverride = s
	return func() { hostPlatformOverride = prev }
}

// fileSuffixPlatform returns the platform-suffix component of basename
// (e.g. "main_macos.zg" → "macos") if the basename ends with one of the
// recognized `_<platform>.zg` suffixes. Returns "" otherwise — including
// for plain "main.zg", for unsupported suffixes like "main_freebsd.zg",
// for non-.zg names, and for stems that are *just* "_<platform>" with no
// module-name prefix.
//
// The match is exact on the closed platformSuffixes set so a filename like
// "macos_notes.zg" (suffix is .zg, not _macos.zg) does not accidentally
// gate.
func fileSuffixPlatform(basename string) string {
	if !strings.HasSuffix(basename, ".zg") {
		return ""
	}
	stem := strings.TrimSuffix(basename, ".zg")
	for _, p := range platformSuffixes {
		if strings.HasSuffix(stem, "_"+p) {
			// Guard against a file literally named "_macos.zg" — a stem
			// equal to "_<p>" has no leading module name. Treat as no
			// suffix so the loader's not-found diagnostic fires uniformly.
			if len(stem) == len(p)+1 {
				return ""
			}
			return p
		}
	}
	return ""
}

// resolveSiblingPath picks the on-disk path for `import "<modulePath>"`
// from a file living in siblingDir.
//
// Lookup order (when the host has a recognized platform suffix):
//  1. siblingDir/<modulePath>_<host>.zg
//  2. siblingDir/<modulePath>.zg
//
// If both files exist on a host-matching platform, the import is
// ambiguous and the caller surfaces an error. If only the wrong-host
// suffix exists (e.g. only `x_linux.zg` on macOS), it is skipped — the
// returned path is the unsuffixed candidate, and the regular not-found
// machinery in loadSibling fires when that file is missing too. The
// wrong-platform diagnostic is reserved for entry-file mismatch; making
// it fire for siblings would punish users who simply forgot a fallback
// shim or who target a single platform.
//
// On unsupported hosts (hostPlatform() == ""), only step 2 is attempted.
// All `_<known>.zg` files are invisible there.
func resolveSiblingPath(siblingDir, modulePath string) (string, error) {
	basePath := filepath.Join(siblingDir, modulePath+".zg")
	host := hostPlatform()
	if host == "" {
		return basePath, nil
	}
	hostPath := filepath.Join(siblingDir, modulePath+"_"+host+".zg")
	hostExists := fileExists(hostPath)
	baseExists := fileExists(basePath)
	if hostExists && baseExists {
		return "", fmt.Errorf(
			"module %q matches both %s.zg and %s_%s.zg; choose one",
			modulePath, modulePath, modulePath, host)
	}
	if hostExists {
		return hostPath, nil
	}
	// Either basePath exists, or neither does — loadSibling reads the
	// returned path and emits the standard not-found diagnostic when it
	// can't open it. We deliberately do NOT switch to a wrong-platform
	// diagnostic if only a wrong-host suffix exists: a sibling miss is a
	// missing file, regardless of who put _linux.zg next to it.
	return basePath, nil
}

// fileExists reports whether the path resolves to a readable filesystem
// entry. The intent is "would os.ReadFile succeed?" — symlinks, regular
// files, anything with read permission. Used only for the resolveSiblingPath
// lookup chain, where a false positive (file exists but unreadable) gets
// caught by the subsequent ReadFile call and surfaces a clearer diagnostic.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
