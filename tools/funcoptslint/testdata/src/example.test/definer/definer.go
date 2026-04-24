// Package definer declares an exported struct with a functional-options
// constructor. Cross-package composite-literal instantiation MUST fail
// funcoptslint.
package definer

type Client struct {
	URL   string // exported so tests can build non-empty literals
	Token string
	url   string // unexported internal state
}

type Option func(*Client)

func WithURL(u string) Option { return func(c *Client) { c.url = u } }

func New(opts ...Option) *Client {
	c := &Client{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NonStruct is an exported type that is NOT a struct. The analyzer must not
// misfire on it.
type NonStruct map[string]string
