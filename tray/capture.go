package main

import (
	"errors"
	"image"

	"github.com/kbinani/screenshot"
)

// captureScreen captura a tela principal (monitor 0).
func captureScreen() (image.Image, error) {
	if screenshot.NumActiveDisplays() < 1 {
		return nil, errors.New("nenhuma tela ativa")
	}
	return screenshot.CaptureDisplay(0)
}
