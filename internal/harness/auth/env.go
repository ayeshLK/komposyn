// Package auth resolves explicit provider-scoped credential references.
package auth

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var ErrMissingCredential = errors.New("credential reference is unavailable")

type Resolver interface {
	Resolve(reference string) (string, error)
}

type EnvironmentResolver struct {
	LookupEnv func(string) (string, bool)
}

func (r EnvironmentResolver) Resolve(reference string) (string, error) {
	if !strings.HasPrefix(reference, "env:") {
		return "", fmt.Errorf("unsupported credential reference (expected env:NAME)")
	}
	name := strings.TrimPrefix(reference, "env:")
	if !envName.MatchString(name) {
		return "", fmt.Errorf("invalid environment-variable reference")
	}
	lookup := r.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: %s", ErrMissingCredential, name)
	}
	return value, nil
}

func ValidateReference(reference string) error {
	if !strings.HasPrefix(reference, "env:") || !envName.MatchString(strings.TrimPrefix(reference, "env:")) {
		return errors.New("credential reference must be env:VARIABLE_NAME")
	}
	return nil
}
