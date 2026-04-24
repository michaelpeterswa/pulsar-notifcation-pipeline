// Package thirdparty stands in for a type defined OUTSIDE the analyzer's
// configured module prefix. Composite literals of these types MUST NOT trip
// the analyzer (we never police third-party code).
package thirdparty

type ExternalConfig struct {
	URL string
}
