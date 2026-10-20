package scrub

import (
	"regexp"
	"strings"
)

// secretWords make a JSON key secret-like when its lower-cased name, without
// _ - and ., holds one of them.
var secretWords = []string{
	"password", "passwd", "passphrase", "secret", "token", "apikey", "privatekey",
	"presharedkey", "credential", "cookie", "csrf", "ticket", "basicauth", "htpasswd",
	"authorization",
}

func secretKey(key string) bool {
	k := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	if k == "psk" {
		return true
	}
	for _, w := range secretWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// redact replaces the value of every secret-like key: a non-empty string,
// or each string of a list, becomes REDACTED. Other values are kept, so a
// flag like "token_set": true still reads the same.
func redact(v any) any {
	switch v := v.(type) {
	case object:
		for i := range v {
			if secretKey(v[i].key) {
				v[i].value = redactValue(v[i].value)
			} else {
				v[i].value = redact(v[i].value)
			}
		}
		return v
	case []any:
		for i := range v {
			v[i] = redact(v[i])
		}
	}
	return v
}

func redactValue(v any) any {
	switch v := v.(type) {
	case string:
		if v == "" {
			return v
		}
		return Redacted
	case []any:
		for i := range v {
			v[i] = redactValue(v[i])
		}
		return v
	case object:
		return redact(v)
	}
	return v
}

var (
	// envSecret is NAME=value with a secret-like NAME, as in a container's
	// environment.
	envSecret = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_?KEY|PRIVATE_?KEY)[A-Z0-9_]*=)[^\s"',;]+`)
	// authHeader is an Authorization header's credentials, e.g. nginx's
	// proxy_set_header Authorization "Basic …".
	authHeader = regexp.MustCompile(`(?i)(authorization"?:?\s+"?(?:basic|bearer|token)\s+)[^\s"',;]+`)
)

// redactText redacts secrets in free text.
func redactText(t string) string {
	t = envSecret.ReplaceAllString(t, "${1}"+Redacted)
	return authHeader.ReplaceAllString(t, "${1}"+Redacted)
}
