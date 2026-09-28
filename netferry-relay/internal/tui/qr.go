package tui

import (
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// renderQR draws content as a QR code (level M, like the desktop dialog)
// using half blocks, two modules per character cell. Colors are forced to
// black-on-white so the code scans regardless of the terminal theme.
func renderQR(content string) (string, int, error) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", 0, err
	}
	bm := q.Bitmap() // includes the quiet zone
	n := len(bm)
	const on, off = "\x1b[30;107m", "\x1b[0m"
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		b.WriteString(on)
		for x := 0; x < n; x++ {
			top := bm[y][x]
			bot := y+1 < n && bm[y+1][x]
			switch {
			case top && bot:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bot:
				b.WriteString("▄")
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString(off)
		if y+2 < n {
			b.WriteByte('\n')
		}
	}
	return b.String(), n, nil
}
