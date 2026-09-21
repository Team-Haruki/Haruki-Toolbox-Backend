package sekai

import (
	"errors"
	"fmt"
)

var (
	ErrNilContent      = errors.New("content cannot be nil")
	ErrEmptyContent    = errors.New("content cannot be empty")
	ErrMaintenance     = errors.New("feature is under maintenance")
	ErrInvalidServer   = errors.New("invalid or unsupported server")
	ErrInvalidDataType = errors.New("invalid or unsupported data type")
)

type APIError struct {
	Endpoint   string
	Method     string
	StatusCode int
	Message    string
	Err        error
}

func (e *APIError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("sekai API error: %s %s returned status %d: %s", e.Method, e.Endpoint, e.StatusCode, e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("sekai API error: %s %s failed: %s: %v", e.Method, e.Endpoint, e.Message, e.Err)
	}
	return fmt.Sprintf("sekai API error: %s %s failed: %s", e.Method, e.Endpoint, e.Message)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

func NewAPIError(endpoint, method string, statusCode int, message string, err error) *APIError {
	return &APIError{
		Endpoint:   endpoint,
		Method:     method,
		StatusCode: statusCode,
		Message:    message,
		Err:        err,
	}
}

type AuthError struct {
	Step    string
	Message string
	Err     error
}

func (e *AuthError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("sekai auth error at %s: %s: %v", e.Step, e.Message, e.Err)
	}
	return fmt.Sprintf("sekai auth error at %s: %s", e.Step, e.Message)
}

func (e *AuthError) Unwrap() error {
	return e.Err
}

func NewAuthError(step, message string, err error) *AuthError {
	return &AuthError{
		Step:    step,
		Message: message,
		Err:     err,
	}
}

type CryptoError struct {
	Operation string
	Message   string
	Err       error
}

func (e *CryptoError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("sekai crypto error during %s: %s: %v", e.Operation, e.Message, e.Err)
	}
	return fmt.Sprintf("sekai crypto error during %s: %s", e.Operation, e.Message)
}

func (e *CryptoError) Unwrap() error {
	return e.Err
}

func NewCryptoError(operation, message string, err error) *CryptoError {
	return &CryptoError{
		Operation: operation,
		Message:   message,
		Err:       err,
	}
}

type DataRetrievalError struct {
	DataType string
	Step     string
	Message  string
	Err      error
}

func (e *DataRetrievalError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("sekai %s retrieval error at %s: %s: %v", e.DataType, e.Step, e.Message, e.Err)
	}
	return fmt.Sprintf("sekai %s retrieval error at %s: %s", e.DataType, e.Step, e.Message)
}

func (e *DataRetrievalError) Unwrap() error {
	return e.Err
}

func NewDataRetrievalError(dataType, step, message string, err error) *DataRetrievalError {
	return &DataRetrievalError{
		DataType: dataType,
		Step:     step,
		Message:  message,
		Err:      err,
	}
}

func IsMaintenanceError(err error) bool {
	return errors.Is(err, ErrMaintenance)
}

func IsAPIError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr)
}

func IsAuthError(err error) bool {
	var authErr *AuthError
	return errors.As(err, &authErr)
}
