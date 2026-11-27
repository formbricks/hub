package service

import (
	"errors"
	"net/url"
	"regexp"
)

// redactedWebhookURL stands in for a webhook URL that cannot be parsed, so not even its origin is known.
const redactedWebhookURL = "[redacted webhook url]"

// RedactWebhookURL returns only the scheme and host (with port) of a webhook URL, for logs and errors.
// For many receivers (Slack, Discord, Teams, Zapier, anything with a ?token= parameter) the path,
// query or userinfo is the credential, and logs reach more people than the tenant's webhook config.
// The webhook_id is the identifier to log; the origin is kept only to tell endpoints apart at a glance.
func RedactWebhookURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return redactedWebhookURL
	}

	return u.Scheme + "://" + u.Host
}

// redactURLError replaces the URL in the *url.Error that url.Parse and http.Client.Do return (its
// Error() quotes the full URL) with the redacted form, and hides quoted text in its cause. Op and the
// cause are kept, so errors.Is, errors.As and Timeout() behave as before. Any other error is
// returned unchanged.
func redactURLError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}

	cause := urlErr.Err
	if cause != nil {
		cause = &quoteRedactedError{err: cause}
	}

	return &url.Error{Op: urlErr.Op, URL: RedactWebhookURL(urlErr.URL), Err: cause}
}

// quotedText matches a Go %q-quoted string, escapes included.
var quotedText = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// quoteRedactedError hides every quoted string in its cause's message. net/http and net/url quote
// the input they failed on, and the receiver controls some of it: http.Client parses a 3xx Location
// before CheckRedirect runs, so a malformed one ends up in the cause as
// `failed to parse Location header "<location>": parse "<location>": …`, which can echo the token.
// Unwrap keeps errors.Is/As working; Timeout is forwarded because url.Error asserts it directly.
type quoteRedactedError struct {
	err error
}

func (e *quoteRedactedError) Error() string {
	return quotedText.ReplaceAllString(e.err.Error(), `"[redacted]"`)
}

func (e *quoteRedactedError) Unwrap() error { return e.err }

func (e *quoteRedactedError) Timeout() bool {
	var t interface{ Timeout() bool }

	return errors.As(e.err, &t) && t.Timeout()
}
