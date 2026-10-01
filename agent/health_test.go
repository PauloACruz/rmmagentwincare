package agent

import (
	"testing"
	"time"
)

func TestScoreHealthIgnoresUnknownAndWeighsWarnings(t *testing.T) {
	r := scoreHealth([]HealthItem{
		{Key: "a", Weight: 10, Status: healthOK},
		{Key: "b", Weight: 10, Status: healthWarning},
		{Key: "c", Weight: 20, Status: healthCritical},
		{Key: "d", Weight: 50, Status: healthUnknown},
	}, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if r.Score != 38 {
		t.Fatalf("score = %d, esperado 38", r.Score)
	}
	if r.Grade != "critico" {
		t.Fatalf("grade = %s", r.Grade)
	}
	if r.Items[1].Points != 5 {
		t.Fatalf("pontos do aviso = %v", r.Items[1].Points)
	}
}

func TestLevelThresholds(t *testing.T) {
	cases := map[float64]string{10: healthOK, 80: healthWarning, 96: healthCritical}
	for v, want := range cases {
		if got := level(v, 80, 95); got != want {
			t.Fatalf("level(%v) = %s, esperado %s", v, got, want)
		}
	}
}
