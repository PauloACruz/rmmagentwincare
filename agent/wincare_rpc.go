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

// HandleWinCareRPC atende wincare_catalog, wincare_run e wincare_cancel (implementacao em wincare_runner.go).
func (a *Agent) HandleWinCareRPC(nc *nats.Conn, msg *nats.Msg, p *NatsMsg) {
	respondString(msg, "error: not implemented")
}

// HandleHealthRPC atende wincare_health (implementacao em health.go).
func (a *Agent) HandleHealthRPC(msg *nats.Msg) {
	respondString(msg, "error: not implemented")
}
