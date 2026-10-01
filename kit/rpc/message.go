// Package rpc implements Ballet's stateful protocol (ADR-0018): JSON-RPC 2.0
// messages over WebSocket, encoded as MessagePack (subprotocol
// "ballet.v1.msgpack") or JSON ("ballet.v1.json"), with authentication in
// the first message, in-band token refresh and heartbeats in both
// directions.
//
// Connections are symmetric: either side can send requests (Call) and
// notifications (Notify) and handle the other side's.
package rpc

import (
	"errors"
	"fmt"
)

// Reserved methods.
const (
	MethodAuth        = "auth"         // first message from the client: {token}
	MethodAuthRefresh = "auth.refresh" // replace the token on an open connection
	MethodHeartbeat   = "$/heartbeat"  // notification, both directions
)

// Error codes: JSON-RPC 2.0 standard codes and Ballet application codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603

	CodeUnauthenticated = -32001
	CodeForbidden       = -32003
	CodeNotFound        = -32004
	CodeConflict        = -32009
)

// Error is a JSON-RPC error. Handlers return it to control the code sent
// to the caller; other errors become CodeInternal with a generic message.
type Error struct {
	Code    int    `json:"code" msgpack:"code"`
	Message string `json:"message" msgpack:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Errorf returns an *Error with code and a formatted message.
func Errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// IsCode reports whether err is an *Error with code.
func IsCode(err error, code int) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// message is the decoded envelope. Params and Result hold values still
// encoded with the connection's codec.
type message struct {
	ID     *int64
	Method string
	Params []byte
	Result []byte
	Error  *Error
}

func (m message) isRequest() bool      { return m.Method != "" && m.ID != nil }
func (m message) isNotification() bool { return m.Method != "" && m.ID == nil }
func (m message) isResponse() bool     { return m.Method == "" && m.ID != nil }

// Heartbeat is the payload of $/heartbeat notifications.
type Heartbeat struct {
	Time int64 `json:"time" msgpack:"time"` // sender's clock, Unix milliseconds
}

// AuthParams are the params of auth and auth.refresh.
type AuthParams struct {
	Token string `json:"token" msgpack:"token"`
}

// AuthResult is the result of auth and auth.refresh.
type AuthResult struct {
	Subject   string `json:"subject" msgpack:"subject"`
	ExpiresAt int64  `json:"expires_at,omitempty" msgpack:"expires_at,omitempty"` // Unix seconds; 0 = no expiry
}
