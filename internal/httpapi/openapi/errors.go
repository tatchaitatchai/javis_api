package openapi

// NewError builds the JSON error envelope shared by every module.
func NewError(code, message string) ErrorResponse {
	return ErrorResponse{Error: Error{Code: code, Message: message}}
}

// Unauthorized is the answer to a missing or expired session.
func Unauthorized() ErrorResponse { return NewError("unauthorized", "Please sign in") }

// Forbidden is the answer when a role does not allow an action.
func Forbidden() ErrorResponse { return NewError("forbidden", "Your role does not allow this") }

// HomeNotFound is the answer for an unknown home and for a non-member alike.
func HomeNotFound() ErrorResponse { return NewError("home_not_found", "Home not found") }
