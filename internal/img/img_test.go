package img

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestHalfBlocksAndPlaceholder(t *testing.T) {
	cellOnce.Do(func() {}) // fixed 10x20 cells, no terminal lookup
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	if cols, rows := Fit(src, 100, 30, Kitty); cols != 40 || rows != 10 {
		t.Errorf("natural kitty size = %dx%d, want 40x10", cols, rows)
	}
	if cols, rows := Fit(src, 20, 30, Kitty); cols != 20 || rows != 5 {
		t.Errorf("width-clamped size = %dx%d, want 20x5", cols, rows)
	}
	if cols, rows := Fit(src, 400, 5, HalfBlock); cols != 20 || rows != 5 {
		t.Errorf("height-clamped size = %dx%d, want 20x5", cols, rows)
	}
	if cols, _ := Fit(image.NewRGBA(image.Rect(0, 0, 48, 48)), 100, 30, Kitty); cols != 5 {
		t.Errorf("small icon cols = %d, want 5 (not stretched)", cols)
	}
	out := HalfBlocks(src, 20, 5, color.Black)
	if lines := strings.Split(out, "\n"); len(lines) != 5 || lipgloss.Width(lines[0]) != 20 {
		t.Errorf("halfblock lines=%d width=%d", len(lines), lipgloss.Width(lines[0]))
	}
	ph := KittyPlaceholder(ID("x"), 20, 5)
	if lines := strings.Split(ph, "\n"); len(lines) != 5 || lipgloss.Width(lines[0]) != 20 {
		t.Errorf("placeholder lines=%d width=%d", len(lines), lipgloss.Width(lines[0]))
	}
	if b := KittyTransmit(src, ID("x"), 20, 5, true); !strings.HasPrefix(string(b), "\x1bPtmux;\x1b\x1b_Ga=T,U=1") {
		t.Errorf("unexpected transmit prefix %q", b[:30])
	}
}
