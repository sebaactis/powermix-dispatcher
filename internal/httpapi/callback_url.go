package httpapi

import (
	"net/url"
	"path"
	"strings"
)

func BatidosCallbackURL(baseURL, secret, orderID string) string {
	if baseURL == "" || secret == "" || orderID == "" {
		return ""
	}

	return strings.TrimSuffix(baseURL, "/") +
		"/callbacks/batidos/" + url.PathEscape(secret) + "/" + url.PathEscape(orderID)
}

func redactCallbackPath(raw string) string {
	if raw == "" {
		return ""
	}

	normalized := unescapePathBounded(raw)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	cleaned := path.Clean("/" + strings.TrimLeft(normalized, "/"))
	if cleaned == "/" {
		return "/"
	}

	segments := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	for i, seg := range segments {
		if strings.EqualFold(seg, "callbacks") {
			if i+1 >= len(segments) {
				return cleaned
			}
			return "/" + strings.Join(append(segments[:i+1], "***"), "/")
		}
	}

	return cleaned
}

func unescapePathBounded(raw string) string {
	cur := raw
	for range 4 {
		next, err := url.PathUnescape(cur)
		if err != nil {
			parts := strings.Split(cur, "/")
			for i, part := range parts {
				if decoded, segErr := url.PathUnescape(part); segErr == nil {
					parts[i] = decoded
				}
			}
			next = strings.Join(parts, "/")
		}
		if next == cur {
			break
		}
		cur = next
	}
	return cur
}
