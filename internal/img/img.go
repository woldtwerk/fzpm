// Package img renders images for the terminal, either as Unicode half blocks
// (works everywhere, including tmux) or via the kitty graphics protocol with
// Unicode placeholders (kitty, ghostty, wezterm).
package img

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"sync"

	"golang.org/x/image/draw"
)

// Mode selects how images are drawn.
type Mode int

const (
	Off Mode = iota
	HalfBlock
	Kitty
)

func (m Mode) String() string {
	return [...]string{"off", "halfblock", "kitty"}[m]
}

// ParseMode parses "off", "halfblock", "kitty" or "auto".
func ParseMode(s string) (Mode, error) {
	switch s {
	case "", "auto":
		return Detect(), nil
	case "off":
		return Off, nil
	case "halfblock":
		return HalfBlock, nil
	case "kitty":
		return Kitty, nil
	}
	return Off, fmt.Errorf("unknown image mode %q (auto|halfblock|kitty|off)", s)
}

// Detect asks the terminal whether it speaks the kitty graphics protocol
// (the same check chafa, yazi or kitty's icat use) and falls back to half
// blocks. Inside tmux the query is sent through passthrough, so it also works
// when tmux (and anything in between, like nvim's :terminal) relays it.
func Detect() Mode {
	tmux := os.Getenv("TMUX") != ""
	if tmux && !tmuxPassthrough() {
		return HalfBlock
	}
	if queryKitty(tmux) {
		return Kitty
	}
	return HalfBlock
}

func tmuxPassthrough() bool {
	for _, scope := range []string{"-pv", "-wv", "-gv"} {
		out, err := exec.Command("tmux", "show", scope, "allow-passthrough").Output()
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(out)); v != "" {
			return v == "on" || v == "all"
		}
	}
	return false
}

// Fit returns the cell size (cols x rows) to draw img at roughly its natural
// pixel size, shrunk to fit maxCols x maxRows. Half blocks may upscale 2x
// since they are coarse anyway.
func Fit(im image.Image, maxCols, maxRows int, mode Mode) (cols, rows int) {
	b := im.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	cw, ch := CellSize()
	natural := (b.Dx() + cw - 1) / cw
	if mode != Kitty {
		natural = min(b.Dx(), natural*2)
	}
	cols = min(maxCols, natural)
	rows = (cols*cw*b.Dy()/b.Dx() + ch - 1) / ch
	if rows > maxRows {
		rows = maxRows
		cols = rows * ch * b.Dx() / (b.Dy() * cw)
	}
	return max(cols, 1), max(rows, 1)
}

// HalfBlocks renders img as cols x rows cells of '▀' with 24-bit colours,
// blending transparency onto bg.
func HalfBlocks(img image.Image, cols, rows int, bg color.Color) string {
	dst := image.NewRGBA(image.Rect(0, 0, cols, rows*2))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	var sb strings.Builder
	sb.Grow(cols * rows * 40)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			t := dst.RGBAAt(x, 2*y)
			u := dst.RGBAAt(x, 2*y+1)
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm▀", t.R, t.G, t.B, u.R, u.G, u.B)
		}
		sb.WriteString("\x1b[0m")
		if y < rows-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// ID derives a stable non-zero 24-bit kitty image id.
func ID(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	id := h.Sum32() & 0xFFFFFF
	if id == 0 {
		id = 1
	}
	return id
}

// KittyTransmit returns the escape sequence that uploads img and creates a
// virtual placement of cols x rows cells. Write it straight to the terminal.
func KittyTransmit(img image.Image, id uint32, cols, rows int, tmux bool) []byte {
	// Downscale to what the placement can show (~20 px per cell covers
	// HiDPI); this keeps uploads small, which matters when they are relayed
	// through tmux and nvim.
	if maxW := cols * 20; img.Bounds().Dx() > maxW {
		b := img.Bounds()
		dst := image.NewRGBA(image.Rect(0, 0, maxW, max(1, maxW*b.Dy()/b.Dx())))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil
	}
	data := base64.StdEncoding.EncodeToString(pngBuf.Bytes())
	var out bytes.Buffer
	for i := 0; i < len(data); i += 4096 {
		chunk := data[i:min(i+4096, len(data))]
		more := 0
		if i+4096 < len(data) {
			more = 1
		}
		var seq string
		if i == 0 {
			seq = fmt.Sprintf("\x1b_Ga=T,U=1,f=100,q=2,i=%d,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, chunk)
		} else {
			seq = fmt.Sprintf("\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
		if tmux {
			seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
		}
		out.WriteString(seq)
	}
	return out.Bytes()
}

// rowDiacritics are the first entries of kitty's rowcolumn-diacritics table.
var rowDiacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F, 0x0346, 0x034A,
	0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357, 0x035B, 0x0363, 0x0364, 0x0365,
	0x0366, 0x0367, 0x0368, 0x0369, 0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F,
}

// MaxKittyRows is the tallest placeholder image we can address.
var MaxKittyRows = len(rowDiacritics)

// KittyPlaceholder returns the Unicode placeholder text for a virtual
// placement. Only the first cell of each row carries diacritics; kitty infers
// the rest (same row, column+1).
func KittyPlaceholder(id uint32, cols, rows int) string {
	rows = min(rows, MaxKittyRows)
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", id>>16&0xFF, id>>8&0xFF, id&0xFF)
	var sb strings.Builder
	for y := 0; y < rows; y++ {
		sb.WriteString(fg)
		sb.WriteRune(0x10EEEE)
		sb.WriteRune(rowDiacritics[y])
		sb.WriteRune(rowDiacritics[0])
		sb.WriteString(strings.Repeat(string(rune(0x10EEEE)), cols-1))
		sb.WriteString("\x1b[39m")
		if y < rows-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

var (
	cellOnce sync.Once
	cellW    = 10
	cellH    = 20
)

// CellSize returns the terminal cell size in pixels: from the window size
// ioctl, then tmux, else a 10x20 guess (nvim's :terminal reports nothing).
func CellSize() (w, h int) {
	cellOnce.Do(func() {
		if w, h := ttyCellSize(); w > 0 && h > 0 {
			cellW, cellH = w, h
			return
		}
		if os.Getenv("TMUX") != "" {
			out, err := exec.Command("tmux", "display", "-p", "#{client_cell_width} #{client_cell_height}").Output()
			var w, h int
			if err == nil {
				fmt.Sscan(string(out), &w, &h)
			}
			if w > 0 && h > 0 {
				cellW, cellH = w, h
			}
		}
	})
	return cellW, cellH
}
