// Package consumer exercises the funcoptslint analyzer. Expected diagnostics
// are declared with analysistest directives at the end of each offending line.
package consumer

import "example.test/definer"

// OK: empty composite literal is exempt.
func OKEmptyLiteral() *definer.Client {
	return &definer.Client{}
}

// BAD: cross-package, exported struct, non-empty literal.
func BadNonEmpty() *definer.Client {
	return &definer.Client{URL: "https://x"} // want "construct Client via definer.NewClient"
}

// BAD: value (non-pointer) form.
func BadValue() definer.Client {
	return definer.Client{Token: "xyz"} // want "construct Client via definer.NewClient"
}

// OK: using the constructor.
func OKConstructor() *definer.Client {
	return definer.New()
}

// OK: NonStruct is not a struct type.
func OKNonStruct() definer.NonStruct {
	return definer.NonStruct{"a": "b"}
}
