// Package thirdparty_consumer constructs a type from outside the analyzer's
// module prefix; this MUST NOT trigger a diagnostic.
package thirdparty_consumer

import "other.test/thirdparty"

func OK() *thirdparty.ExternalConfig {
	return &thirdparty.ExternalConfig{URL: "https://x"}
}
