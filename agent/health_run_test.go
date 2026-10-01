//go:build linux && healthlive
// +build linux,healthlive

package agent

import (
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestCollectHealthLive(t *testing.T) {
	a := &Agent{Logger: logrus.New()}
	data, _ := json.MarshalIndent(a.CollectHealth(), "", "  ")
	t.Log(string(data))
}
