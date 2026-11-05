// Package smssender contains the implementations of auth.SMSSender.
package smssender

import "context"

// Debug sends nothing. The auth service returns the code to the caller instead,
// and config refuses this sender outside APP_ENV=development.
type Debug struct{}

func (Debug) Send(context.Context, string, string) error { return nil }
