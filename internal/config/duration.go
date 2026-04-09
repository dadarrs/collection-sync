package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseHumanDuration parses a human-readable duration string such as "10m",
// "6h", or "3d". An empty string returns zero (meaning run once). The "d"
// suffix is converted to multiples of 24h since time.ParseDuration does not
// support days natively.
func ParseHumanDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// Try the standard Go duration parser first (handles h, m, s, ms, etc.).
	d, err := time.ParseDuration(s)
	if err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("invalid interval %q: must be positive", s)
		}
		return d, nil
	}

	// Fall back to day suffix (e.g. "3d") which time.ParseDuration does not support.
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		days, parseErr := strconv.Atoi(numStr)
		if parseErr == nil {
			if days <= 0 {
				return 0, fmt.Errorf("invalid interval %q: must be positive", s)
			}
			maxDays := int64(time.Duration(1<<63-1) / (24 * time.Hour))
			if int64(days) > maxDays {
				return 0, fmt.Errorf("invalid interval %q: exceeds maximum supported duration", s)
			}
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}

	return 0, fmt.Errorf("invalid interval %q: expected a duration like 10m, 1h, 6h, or 3d", s)
}
