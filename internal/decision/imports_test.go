package decision_test

import (
	"go/build"
	"strings"
	"testing"
)

func TestDecisionPackageHasNoRuntimeOrAppDependencies(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range pkg.Imports {
		for _, forbidden := range []string{"internal/run", "internal/comms", "internal/memory", "internal/app", "internal/codex"} {
			if strings.HasSuffix(imported, "/"+forbidden) || imported == forbidden {
				t.Errorf("forbidden import %q", imported)
			}
		}
	}
}
