package httpapi

import (
	"net/url"
	"strings"
)

func BatidosCallbackURL(baseURL, secret, orderID string) string {
	if baseURL == "" || secret == "" || orderID == "" {
		return ""
	}

	return strings.TrimSuffix(baseURL, "/") +
		"/callbacks/batidos/" + url.PathEscape(secret) + "/" + url.PathEscape(orderID)
}

func redactCallbackPath(path string) string {
	const prefix = "/callbacks/"

	if !strings.HasPrefix(path, prefix) {
		return path
	}

	source, _, found := strings.Cut(path[len(prefix):], "/")
	if !found || source == "" {
		return path
	}

	return prefix + source + "/***"
}
