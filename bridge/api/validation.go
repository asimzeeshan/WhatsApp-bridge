package api

import (
	"regexp"
	"strings"
)

var (
	jidIndividualRe = regexp.MustCompile(`^\d{10,15}@s\.whatsapp\.net$`)
	jidGroupRe      = regexp.MustCompile(`^\d+@g\.us$`)
	jidLidRe        = regexp.MustCompile(`^\d+@lid$`)
)

func isValidJID(jid string) bool {
	return jidIndividualRe.MatchString(jid) || jidGroupRe.MatchString(jid) || jidLidRe.MatchString(jid)
}

func isGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
}

// normalizePhone strips non-digits and appends @s.whatsapp.net.
// Returns the JID and true if valid, or empty string and false.
func normalizePhone(phone string) (string, bool) {
	// Already a JID
	if strings.Contains(phone, "@") {
		return phone, isValidJID(phone)
	}

	var digits strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}

	d := digits.String()
	if len(d) < 10 {
		return "", false
	}

	return d + "@s.whatsapp.net", true
}
