package apperr

import "fmt"

type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

var (
	ErrNotFound     = &Error{Code: "not_found"}
	ErrConflict     = &Error{Code: "conflict"}
	ErrInvalidInput = &Error{Code: "invalid_input"}
	ErrInternal     = &Error{Code: "internal"}
)

func NotFound(msg string, cause error) *Error {
	return &Error{Code: "not_found", Message: msg, Err: cause}
}

func Conflict(msg string, cause error) *Error {
	return &Error{Code: "conflict", Message: msg, Err: cause}
}

func InvalidInput(msg string, cause error) *Error {
	return &Error{Code: "invalid_input", Message: msg, Err: cause}
}

func Internal(msg string, cause error) *Error {
	return &Error{Code: "internal", Message: msg, Err: cause}
}
