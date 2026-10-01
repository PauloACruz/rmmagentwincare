package agent

import (
	nats "github.com/nats-io/nats.go"
	"github.com/ugorji/go/codec"
)

// respondString responde a uma requisicao NATS com uma string em msgpack, no mesmo formato dos demais comandos.
func respondString(msg *nats.Msg, value string) {
	var resp []byte
	ret := codec.NewEncoderBytes(&resp, new(codec.MsgpackHandle))
	_ = ret.Encode(value)
	_ = msg.Respond(resp)
}
