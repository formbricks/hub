package service

import (
	"errors"
	"net/url"
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
// Error() quotes the full URL) with the redacted form. Op and the cause are kept, so errors.Is,
// errors.As and Timeout() behave as before. Any other error is returned unchanged.
func redactURLError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}

	return &url.Error{Op: urlErr.Op, URL: RedactWebhookURL(urlErr.URL), Err: urlErr.Err}
}
