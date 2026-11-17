package sanitizer

import "errors"

var (
	ErrInvalidPolicy   = errors.New("invalid sanitizer policy")
	ErrUnsafeField     = errors.New("unsafe response field")
	ErrPageTooLarge    = errors.New("sanitized finding page exceeds safety bound")
	ErrSummaryTooLarge = errors.New("sanitized finding summary exceeds safety bound")
)
