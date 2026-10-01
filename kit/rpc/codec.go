package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/coder/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// Subprotocols, in server preference order.
const (
	SubprotocolMsgpack = "ballet.v1.msgpack"
	SubprotocolJSON    = "ballet.v1.json"
)

// Subprotocols lists the supported subprotocols, preferred first.
var Subprotocols = []string{SubprotocolMsgpack, SubprotocolJSON}

type codec interface {
	frameType() websocket.MessageType
	encode(m message) ([]byte, error)
	decode(data []byte) (message, error)
	marshal(v any) ([]byte, error)
	unmarshal(data []byte, v any) error
}

func codecFor(subprotocol string) (codec, error) {
	switch subprotocol {
	case SubprotocolMsgpack:
		return msgpackCodec{}, nil
	case SubprotocolJSON:
		return jsonCodec{}, nil
	}
	return nil, fmt.Errorf("unsupported subprotocol %q", subprotocol)
}

// JSON codec.

type jsonCodec struct{}

type jsonWire struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (jsonCodec) frameType() websocket.MessageType { return websocket.MessageText }

func (jsonCodec) encode(m message) ([]byte, error) {
	return json.Marshal(jsonWire{JSONRPC: "2.0", ID: m.ID, Method: m.Method, Params: m.Params, Result: m.Result, Error: m.Error})
}

func (jsonCodec) decode(data []byte) (message, error) {
	var w jsonWire
	if err := json.Unmarshal(data, &w); err != nil {
		return message{}, err
	}
	if w.JSONRPC != "2.0" {
		return message{}, fmt.Errorf("jsonrpc must be \"2.0\"")
	}
	return message{ID: w.ID, Method: w.Method, Params: w.Params, Result: w.Result, Error: w.Error}, nil
}

func (jsonCodec) marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (jsonCodec) unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		data = []byte("null")
	}
	return json.Unmarshal(data, v)
}

// MessagePack codec: the same envelope as a msgpack map.

type msgpackCodec struct{}

type msgpackWire struct {
	JSONRPC string             `msgpack:"jsonrpc"`
	ID      *int64             `msgpack:"id,omitempty"`
	Method  string             `msgpack:"method,omitempty"`
	Params  msgpack.RawMessage `msgpack:"params,omitempty"`
	Result  msgpack.RawMessage `msgpack:"result,omitempty"`
	Error   *Error             `msgpack:"error,omitempty"`
}

func (msgpackCodec) frameType() websocket.MessageType { return websocket.MessageBinary }

func (msgpackCodec) encode(m message) ([]byte, error) {
	return msgpack.Marshal(msgpackWire{JSONRPC: "2.0", ID: m.ID, Method: m.Method, Params: m.Params, Result: m.Result, Error: m.Error})
}

func (msgpackCodec) decode(data []byte) (message, error) {
	var w msgpackWire
	if err := msgpack.Unmarshal(data, &w); err != nil {
		return message{}, err
	}
	if w.JSONRPC != "2.0" {
		return message{}, fmt.Errorf("jsonrpc must be \"2.0\"")
	}
	return message{ID: w.ID, Method: w.Method, Params: w.Params, Result: w.Result, Error: w.Error}, nil
}

func (msgpackCodec) marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := msgpack.NewEncoder(&buf)
	enc.SetCustomStructTag("json") // reuse json tags for payload structs
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (msgpackCodec) unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	dec := msgpack.NewDecoder(bytes.NewReader(data))
	dec.SetCustomStructTag("json")
	return dec.Decode(v)
}
