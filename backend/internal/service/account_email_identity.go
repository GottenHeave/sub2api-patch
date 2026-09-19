package service

import (
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
)

var errAccountEmailIdentityUnavailable = errors.New("a valid account email is required for session identity")

func canonicalAccountEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email {
		return "", errAccountEmailIdentityUnavailable
	}
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return "", errAccountEmailIdentityUnavailable
	}
	return email[:at+1] + strings.ToLower(email[at+1:]), nil
}

// The caller supplies the credential owner. Local row and cache keys are not
// identity inputs, and tuple encoding keeps purpose and session boundaries intact.
func accountEmailIdentitySeed(account *Account, purpose string, components ...string) (string, error) {
	if account == nil {
		return "", errAccountEmailIdentityUnavailable
	}
	email := account.GetCredential("email")
	workspace := ""
	organization := ""
	if account.Platform == PlatformAnthropic {
		email = firstStringValue(account.Extra, "email_address", "email")
		if email == "" {
			email = account.GetCredential("email")
		}
		workspace = strings.TrimSpace(account.GetExtraString("account_uuid"))
		organization = strings.TrimSpace(account.GetExtraString("org_uuid"))
	} else {
		if strings.TrimSpace(email) == "" {
			email = firstStringValue(account.Extra, "email", "email_address")
		}
		workspace = strings.TrimSpace(account.GetChatGPTAccountID())
	}
	email, err := canonicalAccountEmail(email)
	if err != nil {
		return "", err
	}
	parts := []string{"account-email-identity-v1", account.Platform, purpose, email, workspace, organization}
	parts = append(parts, components...)
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
