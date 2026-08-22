package controlcenter

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

type CodedError struct {
	Code string
	Msg  string
	Err  error
}

func (e *CodedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *CodedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func ErrorCode(err error) string {
	var coded *CodedError
	if err == nil {
		return ""
	}
	if errorsAs(err, &coded) {
		return coded.Code
	}
	return ""
}

func NewError(code, message string) error {
	return &CodedError{Code: code, Msg: message}
}

func WrapError(code, message string, err error) error {
	return &CodedError{Code: code, Msg: message, Err: err}
}

func ClampLimit(limit, fallback, min, max int) int {
	if limit == 0 {
		limit = fallback
	}
	if limit < min {
		return min
	}
	if limit > max {
		return max
	}
	return limit
}

func EncodeOffsetCursor(offset int) string {
	if offset <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func DecodeOffsetCursor(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, NewError("cursor_invalid", "cursor is invalid")
	}
	offset, err := strconv.Atoi(string(raw))
	if err != nil || offset < 0 {
		return 0, NewError("cursor_invalid", "cursor is invalid")
	}
	return offset, nil
}

func errorsAs(err error, target **CodedError) bool {
	for err != nil {
		if coded, ok := err.(*CodedError); ok {
			*target = coded
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

type SanitizedExport struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
