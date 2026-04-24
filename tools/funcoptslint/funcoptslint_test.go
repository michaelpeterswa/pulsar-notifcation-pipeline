package funcoptslint_test

import (
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/tools/funcoptslint"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer drives the analyzer against the fixtures under testdata/.
// Fixtures use `// want "..."` comments on the lines that MUST trigger a
// diagnostic; the absence of `want` on any other line MUST NOT trigger one.
func TestAnalyzer(t *testing.T) {
	// Configure the analyzer for the fixture module roots. The default
	// ModulePrefix scopes to the production module; in the fixture sandbox
	// we scope to "example.test/" so `other.test/...` is treated as
	// third-party (never flagged).
	orig := funcoptslint.ModulePrefix
	funcoptslint.ModulePrefix = "example.test/"
	t.Cleanup(func() { funcoptslint.ModulePrefix = orig })

	dir := analysistest.TestData()
	analysistest.Run(t, dir, funcoptslint.Analyzer,
		"example.test/consumer",
		"example.test/selfconstruct",
		"example.test/thirdparty_consumer",
	)
}
