package projects

import (
	"fmt"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// environmentWords are the words that name a deploy environment. A project is
// one product or service; the environment is an attribute of its data (#282).
var environmentWords = []string{"production", "staging", "testing", "prod", "test", "dev", "e2e"}

// environmentSuffix returns the environment word a project name or slug ends
// in, after a separator, or "" when it ends in none. A name that is only an
// environment word has no product in front of it and is not matched.
func environmentSuffix(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	for _, word := range environmentWords {
		if !strings.HasSuffix(v, word) {
			continue
		}
		head := strings.TrimSuffix(v, word)
		if head == "" {
			continue
		}
		if last := head[len(head)-1]; last == '-' || last == '_' || last == ' ' || last == '.' {
			return word
		}
	}
	return ""
}

// rejectEnvironmentName refuses a project whose name or slug ends in an
// environment, and points at the environment field the SDKs set instead.
func rejectEnvironmentName(name, slug string) error {
	for _, value := range []string{name, slug} {
		if word := environmentSuffix(value); word != "" {
			return apperr.InvalidInput(fmt.Sprintf(
				"project %q ends in the environment %q: create one project for the product and send the environment in the "+
					"environment field of each event (SDK option environment, or deployment.environment)", value, word), nil)
		}
	}
	return nil
}
