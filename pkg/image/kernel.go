package image

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Kernel parameter validation
// ---------------------------------------------------------------------------

// Cmdline represents a validated kernel command line.
type Cmdline struct {
	params []param
	raw    string
}

type param struct {
	key   string
	value string // empty for flag-style parameters (e.g. "quiet")
}

// ParseCmdline parses a kernel command line string into a validated Cmdline.
// It accepts space-separated key=value or flag tokens.  Values may be quoted
// with double or single quotes and may contain spaces.
func ParseCmdline(s string) (*Cmdline, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return &Cmdline{}, nil
	}

	tokens, err := splitCmdline(s)
	if err != nil {
		return nil, err
	}

	var params []param
	for _, tok := range tokens {
		p, err := parseParam(tok)
		if err != nil {
			return nil, fmt.Errorf("parse cmdline: %w", err)
		}
		params = append(params, p)
	}

	return &Cmdline{params: params, raw: s}, nil
}

// splitCmdline splits s on whitespace while respecting single and double
// quotes.  It returns an error for unbalanced quotes.
func splitCmdline(s string) ([]string, error) {
	var tokens []string
	var tok strings.Builder
	var inQuote byte // 0, '"', or '\''

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			} else {
				tok.WriteByte(ch)
			}
			continue
		}

		if ch == '"' || ch == '\'' {
			inQuote = ch
			continue
		}

		if ch == ' ' || ch == '\t' || ch == '\n' {
			if tok.Len() > 0 {
				tokens = append(tokens, tok.String())
				tok.Reset()
			}
			continue
		}

		tok.WriteByte(ch)
	}

	if inQuote != 0 {
		return nil, fmt.Errorf("unbalanced quote in cmdline")
	}

	if tok.Len() > 0 {
		tokens = append(tokens, tok.String())
	}

	return tokens, nil
}

// parseParam parses a single token.  Tokens may be:
//
//   - key=value   (e.g. "root=/dev/vda1")
//   - key="value" (quoted, e.g. "ip="10.0.0.1"" )
//   - flag        (e.g. "quiet", "nomodeset")
//
func parseParam(tok string) (param, error) {
	// Quoted value.
	if idx := strings.Index(tok, "="); idx >= 0 {
		key := tok[:idx]
		val := tok[idx+1:]

		if err := validateKey(key); err != nil {
			return param{}, err
		}

		// Strip surrounding quotes.
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}

		return param{key: key, value: val}, nil
	}

	// Flag-style parameter.
	if err := validateKey(tok); err != nil {
		return param{}, err
	}
	return param{key: tok}, nil
}

var keyRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.\-]*$`)

func validateKey(k string) error {
	if k == "" {
		return fmt.Errorf("empty parameter key")
	}
	if !keyRe.MatchString(k) {
		return fmt.Errorf("invalid parameter key %q", k)
	}
	return nil
}

// String rebuilds the validated command line.
func (c *Cmdline) String() string {
	if c.raw != "" {
		return c.raw
	}
	var parts []string
	for _, p := range c.params {
		if p.value == "" {
			parts = append(parts, p.key)
		} else if strings.ContainsAny(p.value, " \\t\n\"") {
			parts = append(parts, fmt.Sprintf("%s=%q", p.key, p.value))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%s", p.key, p.value))
		}
	}
	return strings.Join(parts, " ")
}

// Get returns the value for key, or "" if the key is absent.
// For flags it returns "" even when present; use Has to test presence.
func (c *Cmdline) Get(key string) string {
	for _, p := range c.params {
		if p.key == key {
			return p.value
		}
	}
	return ""
}

// Has reports whether key is present (as a flag or a key=value pair).
func (c *Cmdline) Has(key string) bool {
	for _, p := range c.params {
		if p.key == key {
			return true
		}
	}
	return false
}

// Set replaces the value for key, or appends it if absent.
func (c *Cmdline) Set(key, value string) {
	for i := range c.params {
		if c.params[i].key == key {
			c.params[i].value = value
			c.raw = ""
			return
		}
	}
	c.params = append(c.params, param{key: key, value: value})
	c.raw = ""
}

// AddFlag appends a flag-style parameter.
func (c *Cmdline) AddFlag(key string) {
	c.params = append(c.params, param{key: key})
	c.raw = ""
}

// Validate performs semantic checks on well-known parameters.
func (c *Cmdline) Validate() error {
	if root := c.Get("root"); root != "" {
		if !strings.HasPrefix(root, "/dev/") && !strings.HasPrefix(root, "UUID=") && !strings.HasPrefix(root, "LABEL=") {
			return fmt.Errorf("cmdline: root parameter %q does not look like a valid block device", root)
		}
	}

	if ip := c.Get("ip"); ip != "" {
		if _, _, err := net.ParseCIDR(ip); err != nil {
			// Not CIDR — try plain IP.
			if net.ParseIP(ip) == nil {
				return fmt.Errorf("cmdline: ip parameter %q is not a valid IP address", ip)
			}
		}
	}

	return nil
}

// Params returns a copy of the underlying parameters.
func (c *Cmdline) Params() map[string]string {
	m := make(map[string]string, len(c.params))
	for _, p := range c.params {
		m[p.key] = p.value
	}
	return m
}