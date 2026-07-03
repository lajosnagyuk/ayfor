package page

import (
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/machine"
)

// TestModelVersionsAgree documents, at test level, the invariant the
// compile-time guard in page.go enforces at build time: the version stamped
// into new file headers must equal the version the renderer implements, or
// every newly created document would fail VerifyModel on its next open.
func TestModelVersionsAgree(t *testing.T) {
	if format.ModelVersion != machine.ModelVersion {
		t.Fatalf("model version drift: header stamps v%d, renderer implements v%d - "+
			"new documents would fail to reopen", format.ModelVersion, machine.ModelVersion)
	}
}
