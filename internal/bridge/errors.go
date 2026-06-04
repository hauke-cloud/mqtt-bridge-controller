package bridge

import "errors"

var (
	ErrBridgeNotFound   = errors.New("bridge not found")
	ErrBridgeExists     = errors.New("bridge already exists")
	ErrNotConnected     = errors.New("bridge not connected")
	ErrInvalidConfig    = errors.New("invalid bridge configuration")
	ErrCredentialLookup = errors.New("failed to look up credentials")
)
