package agent

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// partialString decodes the string value of key from JSON that may be cut
// off anywhere, as tool input arrives while the model writes it. It returns
// the text decoded so far and whether the string is complete. An escape that
// is cut off is left out until the rest arrives.
func partialString(raw, key string) (string, bool) {
	i := strings.Index(raw, `"`+key+`"`)
	if i < 0 {
		return "", false
	}
	i += len(key) + 2
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\n' || raw[i] == '\t' || raw[i] == '\r' || raw[i] == ':') {
		i++
	}
	if i >= len(raw) || raw[i] != '"' {
		return "", false
	}
	i++
	var b strings.Builder
	for i < len(raw) {
		c := raw[i]
		switch {
		case c == '"':
			return b.String(), true
		case c != '\\':
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(raw) {
			return b.String(), false
		}
		switch e := raw[i+1]; e {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			r, n, ok := unicodeEscape(raw[i:])
			if !ok {
				return b.String(), false
			}
			b.WriteRune(r)
			i += n
			continue
		default: // " \ / and anything else stand for themselves
			b.WriteByte(e)
		}
		i += 2
	}
	return b.String(), false
}

// unicodeEscape decodes \uXXXX at the start of s, joining a surrogate pair.
// It reports how many bytes it used, or false if s is cut off.
func unicodeEscape(s string) (rune, int, bool) {
	hex := func(s string) (rune, bool) {
		if len(s) < 6 {
			return 0, false
		}
		v, err := strconv.ParseUint(s[2:6], 16, 16)
		return rune(v), err == nil
	}
	r, ok := hex(s)
	if !ok {
		return 0, 0, false
	}
	if utf16.IsSurrogate(r) {
		if len(s) > 6 && s[6] != '\\' { // a lone surrogate
			return utf8.RuneError, 6, true
		}
		if len(s) < 12 {
			return 0, 0, false
		}
		if lo, ok := hex(s[6:]); ok && s[6] == '\\' && s[7] == 'u' {
			return utf16.DecodeRune(r, lo), 12, true
		}
	}
	return r, 6, true
}
