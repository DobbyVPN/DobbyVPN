package sessionapi

import (
    "net/url"
    "strings"
)

// ImportLinkResult describes an app-open or a proposed subscription replacement.
// Parsing does not load a URL, mutate configuration, or start a session.
type ImportLinkResult struct {
    Kind string `json:"kind"`
    Source string `json:"source,omitempty"`
}

const ImportLinkMessage = "Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL"

func ParseImportLink(value string) (ImportLinkResult, error) {
    invalid := func() (ImportLinkResult, error) {
        return ImportLinkResult{}, failure(FailureInvalidArgument, ImportLinkMessage)
    }
    link, err := url.Parse(value)
    if err != nil || !strings.EqualFold(link.Scheme, "dobbyvpn") || link.Opaque != "" ||
        link.User != nil || strings.Contains(value, "#") {
        return invalid()
    }
    // Windows URI activation canonicalizes an empty authority path to slash.
    if link.Path != "" && link.Path != "/" || link.RawPath != "" {
        return invalid()
    }
    if link.Host == "" && link.RawQuery == "" && !link.ForceQuery {
        return ImportLinkResult{Kind: "open"}, nil
    }
    if !strings.EqualFold(link.Host, "import") || strings.Contains(link.RawQuery, "&") ||
        !strings.HasPrefix(link.RawQuery, "url=") {
        return invalid()
    }
    // A literal plus is part of the subscription URL, not a form-encoded space.
    source, err := url.PathUnescape(strings.TrimPrefix(link.RawQuery, "url="))
    if err != nil { return invalid() }
    parsed, err := url.Parse(source)
    if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Opaque != "" || parsed.Hostname() == "" {
        return invalid()
    }
    return ImportLinkResult{Kind: "import", Source: source}, nil
}
