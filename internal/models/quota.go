package models

import "fmt"

type QuotaKey struct {
	ClientID   string
	Route      string
	Method     string
	Identifier string
}

func (qk QuotaKey) String() string {
	return fmt.Sprintf("rl:%s:%s:%s:%s", qk.ClientID, qk.Method, qk.Route, qk.Identifier)
}
