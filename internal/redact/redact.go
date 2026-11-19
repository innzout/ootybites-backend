// Package redact masks personal data before it reaches a log line.
//
// Logs are kept longer and read by more people than the database, so the rule
// is to log what an investigation needs (which account, roughly whose number)
// and nothing that identifies a person on its own.
package redact

// Phone masks all but the last four digits: "9876543210" -> "******3210".
// Enough to tell two customers apart in a log, not enough to call either.
func Phone(p string) string {
	if len(p) <= 4 {
		return "****"
	}
	masked := make([]byte, len(p)-4)
	for i := range masked {
		masked[i] = '*'
	}
	return string(masked) + p[len(p)-4:]
}

// Username trims an attempted login name to a safe length for logging. It is
// attacker-supplied on a failed login, so it is bounded rather than logged
// verbatim.
func Username(u string) string {
	const max = 64
	if len(u) > max {
		return u[:max] + "…"
	}
	return u
}
