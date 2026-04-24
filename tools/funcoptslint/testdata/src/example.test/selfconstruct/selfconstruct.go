// Package selfconstruct verifies that a package is allowed to construct its
// own exported struct via composite literal (no diagnostic).
package selfconstruct

type Inner struct {
	Field string
}

// OwnPackageLiteral must NOT trip the analyzer: the declaring package is
// free to construct its own type however it likes.
func OwnPackageLiteral() *Inner {
	return &Inner{Field: "ok"}
}
