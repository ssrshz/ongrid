package edge

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ongridio/ongrid/internal/pkg/errs"
)

func validateCustomMetricsSpec(spec map[string]interface{}) error {
	if spec == nil {
		return nil
	}
	rawTargets, ok := spec["targets"]
	if !ok {
		return nil
	}
	targets, ok := rawTargets.([]interface{})
	if !ok {
		return fmt.Errorf("%w: custommetrics.targets must be an array", errs.ErrInvalid)
	}
	seenIDs := map[string]struct{}{}
	seenURLs := map[string]string{}
	for i, raw := range targets {
		target, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%w: custommetrics.targets[%d] must be an object", errs.ErrInvalid, i)
		}
		id := mapString(target, "id")
		if id == "" {
			return fmt.Errorf("%w: custommetrics.targets[%d].id required", errs.ErrInvalid, i)
		}
		if _, exists := seenIDs[id]; exists {
			return fmt.Errorf("%w: custommetrics.targets[%d] duplicate id %q", errs.ErrInvalid, i, id)
		}
		seenIDs[id] = struct{}{}
		targetURL := mapString(target, "target_url")
		if targetURL == "" {
			return fmt.Errorf("%w: custommetrics.targets[%d].target_url required", errs.ErrInvalid, i)
		}
		urlKey, err := canonicalCustomMetricsTargetURL(targetURL)
		if err != nil {
			return fmt.Errorf("%w: custommetrics.targets[%d].target_url: %v", errs.ErrInvalid, i, err)
		}
		if prevID, exists := seenURLs[urlKey]; exists {
			return fmt.Errorf("%w: custommetrics.targets[%d] duplicate target_url %q conflicts with target %q", errs.ErrInvalid, i, targetURL, prevID)
		}
		seenURLs[urlKey] = id
	}
	return nil
}

func canonicalCustomMetricsTargetURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}
