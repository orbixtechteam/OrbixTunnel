//go:build !darwin

package main

import "errors"

// The root helper is macOS-only; Windows/Linux run elevated as a whole.
func (s *Service) startTUNDarwin(sb, cfg string) error {
	return errors.New("not supported on this OS")
}

func shutdownHelper() {}
