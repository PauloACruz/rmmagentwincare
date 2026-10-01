// Package realtime e um cliente minimo do hub SignalR /hubs/tray usando o protocolo JSON
// diretamente sobre WebSocket (sem a etapa de negociacao).
package realtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// RecordSeparator termina cada mensagem do protocolo JSON do SignalR.
const RecordSeparator = 0x1E

// Tipos de mensagem do protocolo de hub do SignalR.
const (
	TypeInvocation       = 1
	TypeStreamItem       = 2
	TypeCompletion       = 3
	TypeStreamInvocation = 4
	TypeCancelInvocation = 5
	TypePing             = 6
	TypeClose            = 7
)

// Message e uma mensagem do hub ja decodificada.
type Message struct {
	Type           int               `json:"type"`
	Target         string            `json:"target,omitempty"`
	Arguments      []json.RawMessage `json:"arguments,omitempty"`
	InvocationID   string            `json:"invocationId,omitempty"`
	Error          string            `json:"error,omitempty"`
	AllowReconnect bool              `json:"allowReconnect,omitempty"`
}

// HandshakeRequest e a primeira mensagem enviada pelo cliente.
func HandshakeRequest() []byte {
	return append([]byte(`{"protocol":"json","version":1}`), RecordSeparator)
}

// PingMessage e a mensagem de ping (tipo 6) ja com o separador.
func PingMessage() []byte {
	return append([]byte(`{"type":6}`), RecordSeparator)
}

// SplitRecords separa as mensagens completas de buf e devolve o resto ainda sem separador.
func SplitRecords(buf []byte) (records [][]byte, rest []byte) {
	for {
		i := bytes.IndexByte(buf, RecordSeparator)
		if i < 0 {
			return records, buf
		}
		if i > 0 {
			records = append(records, buf[:i])
		}
		buf = buf[i+1:]
	}
}

// ParseHandshake valida a resposta do servidor ao handshake: "{}" ou {"error":"..."}.
func ParseHandshake(record []byte) error {
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(record, &resp); err != nil {
		return fmt.Errorf("handshake invalido: %w", err)
	}
	if resp.Error != "" {
		return errors.New("handshake recusado: " + resp.Error)
	}
	return nil
}

// ParseMessage decodifica uma mensagem do hub (sem o separador).
func ParseMessage(record []byte) (Message, error) {
	var m Message
	if err := json.Unmarshal(record, &m); err != nil {
		return Message{}, fmt.Errorf("mensagem invalida: %w", err)
	}
	if m.Type == 0 {
		return Message{}, errors.New("mensagem sem tipo")
	}
	return m, nil
}
