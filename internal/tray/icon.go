package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
)

// GenerateIconICO creates a standard 32x32 32-bit BGRA DIB Windows .ICO byte slice
// compatible with Win32 CreateIconFromResourceEx / LoadImage across all Windows 11 builds.
func GenerateIconICO() []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	// Draw rounded square dark background (#161922)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if isOutsideRoundedRect(x, y, size, size, 6) {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{R: 22, G: 25, B: 34, A: 255})
		}
	}

	// Draw 4 vertical network throughput bars (cyan + purple)
	drawBar(img, 5, 18, 9, 26, color.NRGBA{R: 56, G: 189, B: 248, A: 255})  // Cyan bar 1
	drawBar(img, 11, 11, 15, 26, color.NRGBA{R: 56, G: 189, B: 248, A: 255}) // Cyan bar 2
	drawBar(img, 17, 14, 21, 26, color.NRGBA{R: 168, G: 85, B: 247, A: 255}) // Purple bar 3
	drawBar(img, 23, 7, 27, 26, color.NRGBA{R: 56, G: 189, B: 248, A: 255})  // Cyan bar 4

	// Encode as standard Win32 BITMAPINFOHEADER (40 bytes) + bottom-up BGRA pixels + 1-bit AND mask
	const xorBytesLen = size * size * 4
	const andMaskRowBytes = ((size + 31) / 32) * 4 // 4 bytes per row for 32px
	const andBytesLen = andMaskRowBytes * size
	const dibSize = 40 + xorBytesLen + andBytesLen

	var buf bytes.Buffer

	// 1. ICONDIR (6 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // idReserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // idType (1 = ICO)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // idCount (1)

	// 2. ICONDIRENTRY (16 bytes)
	buf.WriteByte(byte(size))                                       // bWidth
	buf.WriteByte(byte(size))                                       // bHeight
	buf.WriteByte(0)                                                // bColorCount
	buf.WriteByte(0)                                                // bReserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))          // wPlanes
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))         // wBitCount
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dibSize))    // dwBytesInRes
	_ = binary.Write(&buf, binary.LittleEndian, uint32(6+16))       // dwImageOffset

	// 3. BITMAPINFOHEADER (40 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(40))               // biSize
	_ = binary.Write(&buf, binary.LittleEndian, int32(size))              // biWidth
	_ = binary.Write(&buf, binary.LittleEndian, int32(size*2))            // biHeight (XOR + AND height)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))                // biPlanes
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))               // biBitCount
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))                // biCompression (BI_RGB)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(xorBytesLen+andBytesLen)) // biSizeImage
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))                 // biXPelsPerMeter
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))                 // biYPelsPerMeter
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))                // biClrUsed
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))                // biClrImportant

	// 4. Bottom-up BGRA pixel data (y from size-1 down to 0)
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			c := img.NRGBAAt(x, y)
			buf.WriteByte(c.B)
			buf.WriteByte(c.G)
			buf.WriteByte(c.R)
			buf.WriteByte(c.A)
		}
	}

	// 5. Bottom-up 1-bit AND mask (0 = opaque, 1 = transparent)
	for y := size - 1; y >= 0; y-- {
		var row [andMaskRowBytes]byte
		for x := 0; x < size; x++ {
			if img.NRGBAAt(x, y).A == 0 {
				row[x/8] |= 1 << (7 - (uint(x) % 8))
			}
		}
		buf.Write(row[:])
	}

	return buf.Bytes()
}

func drawBar(img *image.NRGBA, x1, y1, x2, y2 int, c color.NRGBA) {
	for y := y1; y <= y2; y++ {
		for x := x1; x <= x2; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}

func isOutsideRoundedRect(x, y, w, h, r int) bool {
	if x < r && y < r {
		dx, dy := r-x, r-y
		return dx*dx+dy*dy > r*r
	}
	if x >= w-r && y < r {
		dx, dy := x-(w-r-1), r-y
		return dx*dx+dy*dy > r*r
	}
	if x < r && y >= h-r {
		dx, dy := r-x, y-(h-r-1)
		return dx*dx+dy*dy > r*r
	}
	if x >= w-r && y >= h-r {
		dx, dy := x-(w-r-1), y-(h-r-1)
		return dx*dx+dy*dy > r*r
	}
	return false
}
