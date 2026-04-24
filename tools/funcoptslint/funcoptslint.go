// Package funcoptslint is the custom go/analysis pass that enforces the
// project constitution's Principle II (Functional Options, NON-NEGOTIABLE).
//
// Rule: if an exported struct type T is declared in a package P within this
// module AND P exposes a constructor function named `NewT` or `New` that
// returns T (or *T), then a composite literal `P.T{...}` or `&P.T{...}`
// from OUTSIDE P is a violation. Consumers must call the constructor.
//
// The matching-constructor gate is how the analyzer tells "configurable
// service struct" (MUST use constructor) apart from "plain data record"
// (composite literals are fine). Types without a constructor are never
// flagged.
//
// Exceptions:
//   - Composite literals inside P itself are always allowed (the package
//     implementing the constructor must be able to construct its own type).
//   - Generated code (file path ends in `.gen.go` or `.pb.go`) is exempt —
//     code generators are responsible for their own idioms.
//   - Test files (`*_test.go`) are exempt — white-box tests sometimes need to
//     fabricate fixtures.
//   - Empty composite literals (`&P.T{}`) are exempt: they are indistinguishable
//     from zero-value constructors and are commonly used by mock/spy patterns
//     where the intent is deliberate.
//
// The analyzer only flags composite literals targeting types declared in
// packages whose import path begins with the project's module prefix; standard
// library and third-party types are never flagged.
package funcoptslint

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// ModulePrefix is the module-path prefix that scopes the analyzer. Only
// composite literals targeting types declared under this prefix are
// candidates for diagnostic emission. Overridable via the -module-prefix
// flag (useful if the module is ever renamed or if a fork reuses the code).
var ModulePrefix = "github.com/michaelpeterswa/pulsar-notifcation-pipeline/"

// Analyzer is the externally visible analyzer. Register it with singlechecker
// or golangci-lint to enforce Principle II at build time.
var Analyzer = &analysis.Analyzer{
	Name: "funcoptslint",
	Doc:  "Enforces the functional-options pattern for exported struct constructors (Constitution Principle II).",
	Run:  run,
}

func init() {
	Analyzer.Flags.StringVar(&ModulePrefix, "module-prefix", ModulePrefix,
		"only flag composite literals for types declared in packages with this module-path prefix")
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if isExemptFile(pass, file) {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// Empty literal is always permitted.
			if len(cl.Elts) == 0 {
				return true
			}
			// Only flag cross-package construction: the type expression must
			// be a selector (pkg.T); bare identifiers (T{...}) target types
			// in the current package, which is always allowed.
			sel, ok := cl.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			tv, ok := pass.TypesInfo.Types[cl]
			if !ok {
				return true
			}
			named, ok := typeAsNamedStruct(tv.Type)
			if !ok {
				return true
			}
			// Only consider exported types.
			if !named.Obj().Exported() {
				return true
			}
			declaring := named.Obj().Pkg()
			if declaring == nil {
				return true
			}
			if !strings.HasPrefix(declaring.Path(), ModulePrefix) {
				return true
			}
			// Same package as the caller? Always allowed.
			if declaring.Path() == pass.Pkg.Path() {
				return true
			}
			// Only flag if the declaring package exposes a constructor
			// returning this type. Data records with no constructor are
			// intentionally permissive.
			if !hasConstructor(declaring, named) {
				return true
			}
			pass.ReportRangef(sel, "construct %s via %s.New%s (functional options); direct composite literal violates Constitution Principle II",
				named.Obj().Name(), selectorPkgName(sel), named.Obj().Name())
			return true
		})
	}
	return nil, nil
}

// typeAsNamedStruct returns the underlying *types.Named when t refers to a
// struct type (or a pointer to one). It ignores non-struct named types
// (interfaces, maps, etc. — those cannot be built via struct-composite
// literals anyway).
func typeAsNamedStruct(t types.Type) (*types.Named, bool) {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil, false
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return nil, false
	}
	return named, true
}

// hasConstructor reports whether pkg exposes a function named `New<T>` or
// `New` whose return type is T or *T. This is how we distinguish
// configurable-service structs (which MUST use the constructor) from plain
// data records (which are intentionally constructed inline).
func hasConstructor(pkg *types.Package, named *types.Named) bool {
	typeName := named.Obj().Name()
	candidates := []string{"New" + typeName, "New"}
	scope := pkg.Scope()
	for _, name := range candidates {
		obj := scope.Lookup(name)
		if obj == nil {
			continue
		}
		fn, ok := obj.(*types.Func)
		if !ok {
			continue
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			continue
		}
		results := sig.Results()
		for i := 0; i < results.Len(); i++ {
			rt := results.At(i).Type()
			if ptr, ok := rt.(*types.Pointer); ok {
				rt = ptr.Elem()
			}
			if rt == named {
				return true
			}
		}
	}
	return false
}

func selectorPkgName(s *ast.SelectorExpr) string {
	if id, ok := s.X.(*ast.Ident); ok {
		return id.Name
	}
	return "pkg"
}

func isExemptFile(pass *analysis.Pass, file *ast.File) bool {
	path := pass.Fset.Position(file.Pos()).Filename
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	if strings.HasSuffix(path, ".gen.go") || strings.HasSuffix(path, ".pb.go") {
		return true
	}
	return false
}
