package mail

import "fmt"

// Error is what callers of Send get back on failure. It carries the
// error class and SMTP reply code and nothing the relay said, because callers
// log it and the relay's text can quote recipient addresses or account names.
type Error struct {
	Class string
	Code  int
	cause error
}

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("send email failed: %s (smtp %d)", e.Class, e.Code)
	}
	return "send email failed: " + e.Class
}
