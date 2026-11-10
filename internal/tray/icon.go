package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
)

// GenerateIconICO creates a standard 32x32 32-bit BGRA DIB Windows .ICO byte slice.
func GenerateIconICO() []byte {
	return GenerateActivityIconICO(true, true)
}

// GenerateActivityIconICO creates a 32x32 32-bit BGRA DIB Windows .ICO byte slice
// where the RX (cyan) and TX (purple) bars glow brightly when active or dim when idle.
func GenerateActivityIconICO(rxActive, txActive bool) []byte {
	const size = 32
	img := DrawAppIconImage(size, rxActive, txActive)
	return encodeImagesToICO([]*image.NRGBA{img})
}

// GenerateMultiSizeAppICO generates a high-DPI multi-resolution Windows .ICO (64x64, 32x32, 16x16)
// suitable for embedding into traffcount.exe's PE resources and Web App favicon.
func GenerateMultiSizeAppICO() []byte {
	img64 := DrawAppIconImage(64, true, true)
	img32 := DrawAppIconImage(32, true, true)
	img16 := DrawAppIconImage(16, true, true)
	return encodeImagesToICO([]*image.NRGBA{img64, img32, img16})
}

// DrawAppIconImage renders the TraffCount icon at the given square dimension.
func DrawAppIconImage(size int, rxActive, txActive bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	radius := max(size*6/32, 2)

	// Draw rounded square dark background (#161922) with subtle cyan/purple border
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if isOutsideRoundedRect(x, y, size, size, radius) {
				continue
			}
			// 1px outer accent border
			if isOutsideRoundedRect(x-1, y-1, size-2, size-2, max(radius-1, 1)) {
				img.SetNRGBA(x, y, color.NRGBA{R: 45, G: 110, B: 160, A: 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 18, G: 21, B: 30, A: 255})
			}
		}
	}

	rxCol := color.NRGBA{R: 56, G: 189, B: 248, A: 255} // Bright Cyan
	if !rxActive {
		rxCol = color.NRGBA{R: 38, G: 85, B: 115, A: 210} // Dim Cyan
	}

	txCol := color.NRGBA{R: 178, G: 92, B: 255, A: 255} // Bright Purple
	if !txActive {
		txCol = color.NRGBA{R: 82, G: 50, B: 115, A: 210} // Dim Purple
	}

	scale := func(v int) int {
		return (v * size) / 32
	}

	// 4 vertical network throughput bars (2 RX cyan, 2 TX purple)
	drawBar(img, scale(5), scale(17), max(scale(9), scale(5)+1), scale(26), rxCol)
	drawBar(img, scale(11), scale(10), max(scale(15), scale(11)+1), scale(26), txCol)
	drawBar(img, scale(17), scale(14), max(scale(21), scale(17)+1), scale(26), txCol)
	drawBar(img, scale(23), scale(6), max(scale(27), scale(23)+1), scale(26), rxCol)

	return img
}

func encodeImagesToICO(images []*image.NRGBA) []byte {
	var dibPayloads [][]byte
	for _, img := range images {
		dibPayloads = append(dibPayloads, encodeDIB(img))
	}

	var buf bytes.Buffer
	// ICONDIR (6 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(images)))

	offset := uint32(6 + 16*len(images))
	for i, img := range images {
		sz := img.Bounds().Dx()
		dib := dibPayloads[i]
		wByte := byte(sz)
		if sz >= 256 {
			wByte = 0
		}
		buf.WriteByte(wByte)
		buf.WriteByte(wByte)
		buf.WriteByte(0)
		buf.WriteByte(0)
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(dib)))
		_ = binary.Write(&buf, binary.LittleEndian, offset)
		offset += uint32(len(dib))
	}

	for _, dib := range dibPayloads {
		buf.Write(dib)
	}
	return buf.Bytes()
}

func encodeDIB(img *image.NRGBA) []byte {
	size := img.Bounds().Dx()
	xorBytesLen := size * size * 4
	andMaskRowBytes := ((size + 31) / 32) * 4
	andBytesLen := andMaskRowBytes * size

	var buf bytes.Buffer
	// BITMAPINFOHEADER (40 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(40))
	_ = binary.Write(&buf, binary.LittleEndian, int32(size))
	_ = binary.Write(&buf, binary.LittleEndian, int32(size*2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(xorBytesLen+andBytesLen))
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))

	// Bottom-up BGRA pixels
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			c := img.NRGBAAt(x, y)
			buf.WriteByte(c.B)
			buf.WriteByte(c.G)
			buf.WriteByte(c.R)
			buf.WriteByte(c.A)
		}
	}

	// Bottom-up 1-bit AND mask
	for y := size - 1; y >= 0; y-- {
		row := make([]byte, andMaskRowBytes)
		for x := 0; x < size; x++ {
			if img.NRGBAAt(x, y).A == 0 {
				row[x/8] |= 1 << (7 - (uint(x) % 8))
			}
		}
		buf.Write(row)
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
