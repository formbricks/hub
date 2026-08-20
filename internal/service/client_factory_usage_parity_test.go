package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEveryProviderClientRecordsUsage walks the factories' own source and asserts that every
// provider-client construction passes a usage recorder.
//
// This is a parity check rather than a behavioural one because the failure it guards against is
// SILENT. A factory that forgets the recorder still builds, still enriches, still passes every
// other test — it just stops reporting what it costs, and nobody notices until someone asks why
// the token dashboard is empty for half the deployments. That is exactly what happened: the
// recorder was threaded through the OpenAI factories and not the Google ones, so any deployment on
// google or google-gemini recorded nothing at all.
//
// Reading the AST rather than grepping means a renamed option or a construction split across lines
// is still matched, and adding a fifth enrichment fails here rather than in production silence.
func TestEveryProviderClientRecordsUsage(t *testing.T) {
	constructors := map[string]bool{
		"openai.NewClient":               true,
		"googleai.NewClient":             true,
		"googleai.NewGoogleGeminiClient": true,
	}

	files, err := filepath.Glob("*_client_factory.go")
	require.NoError(t, err)
	require.NotEmpty(t, files, "the factories must be where this test expects them")

	fset := token.NewFileSet()
	found := 0

	for _, file := range files {
		parsed, parseErr := parser.ParseFile(fset, file, nil, parser.ParseComments)
		require.NoError(t, parseErr)

		ast.Inspect(parsed, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}

			name := callName(call.Fun)
			if !constructors[name] {
				return true
			}

			found++

			require.True(t, passesUsageRecorder(call),
				"%s: %s at line %d constructs a provider client without a usage recorder; "+
					"its tokens and durations would go unreported",
				file, name, fset.Position(call.Pos()).Line)

			return true
		})
	}

	// A guard that matches nothing passes vacuously, which is the failure mode of every
	// source-reading test. Pin the count so a rename that stops matching is itself a failure.
	require.GreaterOrEqual(t, found, 12,
		"expected at least the four OpenAI and eight Google construction sites, found %d — "+
			"if a constructor was renamed, update this test rather than deleting it", found)
}

// callName renders a selector call such as googleai.NewClient as "googleai.NewClient".
func callName(fun ast.Expr) string {
	selector, isSelector := fun.(*ast.SelectorExpr)
	if !isSelector {
		return ""
	}

	pkg, isIdent := selector.X.(*ast.Ident)
	if !isIdent {
		return ""
	}

	return pkg.Name + "." + selector.Sel.Name
}

// passesUsageRecorder reports whether any argument is a WithUsageRecorder option.
func passesUsageRecorder(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		inner, isCall := arg.(*ast.CallExpr)
		if !isCall {
			continue
		}

		if strings.HasSuffix(callName(inner.Fun), ".WithUsageRecorder") {
			return true
		}
	}

	return false
}
