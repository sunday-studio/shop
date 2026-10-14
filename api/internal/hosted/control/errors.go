package control

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Error carries the stable contract code and the HTTP status the thin HTTP layer will use.
type Error struct {
	Code              string
	Status            int
	Msg               string
	RetryAfterSeconds int // rate_limited, apple_unavailable
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func newErr(code string, status int, msg string) *Error {
	return &Error{Code: code, Status: status, Msg: msg}
}

var (
	ErrNotFound                = newErr("not_found", http.StatusNotFound, "not found")
	ErrNoHousehold             = newErr("no_household", http.StatusNotFound, "no household")
	ErrInviteInvalid           = newErr("invite_invalid", http.StatusNotFound, "invite is not valid")
	ErrNotOwner                = newErr("not_household_owner", http.StatusForbidden, "only the household owner can do this")
	ErrAccountLocked           = newErr("account_locked", http.StatusForbidden, "account is locked")
	ErrHouseholdFull           = newErr("household_full", http.StatusConflict, "household is full")
	ErrAlreadyInHousehold      = newErr("already_in_household", http.StatusConflict, "account already belongs to a household")
	ErrOwnerCannotLeave        = newErr("owner_cannot_leave", http.StatusConflict, "the owner cannot leave")
	ErrCannotRemoveOwner       = newErr("cannot_remove_owner", http.StatusConflict, "the owner cannot be removed")
	ErrTransferTargetNoPlan    = newErr("transfer_target_no_plan", http.StatusConflict, "transfer target has no plan")
	ErrOwnerMustConfirm        = newErr("owner_must_confirm_household", http.StatusConflict, "owner must confirm household deletion")
	ErrNotHouseholdOwnerDelete = newErr("not_household_owner", http.StatusConflict, "member cannot delete the household")
	ErrDeletionNotPending      = newErr("deletion_not_pending", http.StatusConflict, "no deletion is pending")
	ErrInvalidRequest          = newErr("invalid_request", http.StatusBadRequest, "invalid request")
	ErrLimitReached            = newErr("limit_reached", http.StatusTooManyRequests, "limit reached")
)

// Code returns the contract code of err, or "" when err is not an *Error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if le, ok := IsLimit(err); ok {
		return le.ErrCode()
	}
	return ""
}

// Is reports whether err is (wraps) the given sentinel by code.
func Is(err error, target *Error) bool { return Code(err) == target.Code }

// LimitError is a quota hit (contract code limit_reached, HTTP 429).
type LimitError struct {
	Limit    string // receipts_per_month, receipts_per_day, storage_bytes, max_queued_jobs, exports_per_day, open_invites
	Value    int64  // the limit
	Current  int64
	ResetsAt time.Time // zero for non-periodic limits
	// FileTooLarge turns the error into code file_too_large (HTTP 413) for max_upload_bytes.
	FileTooLarge bool
}

// ErrCode is the contract code.
func (e *LimitError) ErrCode() string {
	if e.FileTooLarge {
		return "file_too_large"
	}
	return "limit_reached"
}

// HTTPStatus is 413 for file_too_large, else 429.
func (e *LimitError) HTTPStatus() int {
	if e.FileTooLarge {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusTooManyRequests
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("limit_reached: %s (%d of %d)", e.Limit, e.Current, e.Value)
}

// IsLimit reports whether err is a *LimitError and returns it.
func IsLimit(err error) (*LimitError, bool) {
	var le *LimitError
	if errors.As(err, &le) {
		return le, true
	}
	return nil, false
}
