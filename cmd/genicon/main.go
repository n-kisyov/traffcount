package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"traffcount/internal/tray"
)

func main() {
	icoPath := "cmd/traffcount/app.ico"
	sysoPath := "cmd/traffcount/rsrc_windows_amd64.syso"

	ico := tray.GenerateMultiSizeAppICO()
	if err := os.WriteFile(icoPath, ico, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", icoPath, err)
		os.Exit(1)
	}
	fmt.Printf("Generated multi-size app icon: %s (%d bytes)\n", icoPath, len(ico))

	syso, err := buildCOFFResourceFromICO(ico)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build COFF resource: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(sysoPath, syso, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", sysoPath, err)
		os.Exit(1)
	}
	fmt.Printf("Generated PE resource object: %s (%d bytes)\n", sysoPath, len(syso))
}

// buildCOFFResourceFromICO packs a multi-image .ICO byte slice into a Windows x86_64 COFF (.syso)
// object file with a .rsrc section containing RT_ICON (3) and RT_GROUP_ICON (14).
func buildCOFFResourceFromICO(ico []byte) ([]byte, error) {
	if len(ico) < 6 {
		return nil, fmt.Errorf("ico too short")
	}
	count := int(binary.LittleEndian.Uint16(ico[4:6]))
	if count == 0 || len(ico) < 6+count*16 {
		return nil, fmt.Errorf("invalid ico directory count: %d", count)
	}

	type iconChunk struct {
		id   uint16
		data []byte
	}
	icons := make([]iconChunk, count)

	// GRPICONDIR header (6 bytes) + count * GRPICONDIRENTRY (14 bytes each)
	grpBuf := bytes.NewBuffer(make([]byte, 0, 6+count*14))
	grpBuf.Write(ico[0:6])

	for i := 0; i < count; i++ {
		entOff := 6 + i*16
		ent := ico[entOff : entOff+16]
		size := int(binary.LittleEndian.Uint32(ent[8:12]))
		offset := int(binary.LittleEndian.Uint32(ent[12:16]))
		if offset+size > len(ico) {
			return nil, fmt.Errorf("ico entry %d out of bounds", i)
		}
		iconID := uint16(i + 1)
		icons[i] = iconChunk{
			id:   iconID,
			data: ico[offset : offset+size],
		}
		// GRPICONDIRENTRY is first 12 bytes of ICONDIRENTRY + uint16 resource ID
		grpBuf.Write(ent[0:12])
		_ = binary.Write(grpBuf, binary.LittleEndian, iconID)
	}
	grpData := grpBuf.Bytes()

	// Resource tree layout in .rsrc:
	// Total resources = count (RT_ICON, type=3, IDs 1..count) + 1 (RT_GROUP_ICON, type=14, ID=1)
	totalRes := count + 1

	// Level 1 (Root): 1 dir header (16B) + 2 entries (RT_ICON=3, RT_GROUP_ICON=14) (16B) = 32B
	// Level 2:
	//   Dir for RT_ICON: 16B + count * 8B
	//   Dir for RT_GROUP_ICON: 16B + 1 * 8B = 24B
	// Level 3 (Language dirs, 1 per resource):
	//   totalRes * (16B + 8B) = totalRes * 24B
	// Data entries (IMAGE_RESOURCE_DATA_ENTRY, 16B each):
	//   totalRes * 16B
	lvl1Size := 16 + 2*8
	lvl2IconOff := lvl1Size
	lvl2IconSize := 16 + count*8
	lvl2GrpOff := lvl2IconOff + lvl2IconSize
	lvl2GrpSize := 16 + 1*8
	lvl3StartOff := lvl2GrpOff + lvl2GrpSize
	lvl3TotalSize := totalRes * 24
	dataEntriesOff := lvl3StartOff + lvl3TotalSize
	dataEntriesSize := totalRes * 16
	rawDataStartOff := align8(dataEntriesOff + dataEntriesSize)

	rsrc := bytes.NewBuffer(nil)
	writeDir := func(numIDEntries uint16) {
		_ = binary.Write(rsrc, binary.LittleEndian, uint32(0)) // Characteristics
		_ = binary.Write(rsrc, binary.LittleEndian, uint32(0)) // TimeDateStamp
		_ = binary.Write(rsrc, binary.LittleEndian, uint16(0)) // MajorVersion
		_ = binary.Write(rsrc, binary.LittleEndian, uint16(0)) // MinorVersion
		_ = binary.Write(rsrc, binary.LittleEndian, uint16(0)) // NumberOfNamedEntries
		_ = binary.Write(rsrc, binary.LittleEndian, numIDEntries)
	}
	writeDirEntry := func(id uint32, offset uint32, isSubdir bool) {
		_ = binary.Write(rsrc, binary.LittleEndian, id)
		if isSubdir {
			offset |= 0x80000000
		}
		_ = binary.Write(rsrc, binary.LittleEndian, offset)
	}

	// Level 1: Root directory (types 3 and 14)
	writeDir(2)
	writeDirEntry(3, uint32(lvl2IconOff), true) // RT_ICON
	writeDirEntry(14, uint32(lvl2GrpOff), true) // RT_GROUP_ICON

	// Level 2: RT_ICON directory (IDs 1..count)
	writeDir(uint16(count))
	for i := 0; i < count; i++ {
		lvl3Off := lvl3StartOff + i*24
		writeDirEntry(uint32(icons[i].id), uint32(lvl3Off), true)
	}

	// Level 2: RT_GROUP_ICON directory (ID 1)
	writeDir(1)
	writeDirEntry(1, uint32(lvl3StartOff+count*24), true)

	// Level 3: Language directories (lang 0x0409 = en-US) pointing to IMAGE_RESOURCE_DATA_ENTRY
	for i := 0; i < totalRes; i++ {
		writeDir(1)
		dataEntOff := dataEntriesOff + i*16
		writeDirEntry(0x0409, uint32(dataEntOff), false)
	}

	// Build raw data blobs and record their offsets
	rawBlobs := make([][]byte, totalRes)
	for i := 0; i < count; i++ {
		rawBlobs[i] = icons[i].data
	}
	rawBlobs[count] = grpData

	blobOffsets := make([]uint32, totalRes)
	curRawOff := rawDataStartOff
	for i, b := range rawBlobs {
		blobOffsets[i] = uint32(curRawOff)
		curRawOff = align8(curRawOff + len(b))
	}

	// Write IMAGE_RESOURCE_DATA_ENTRY array (each entry's first uint32 is relocated by COFF relocation)
	for i, b := range rawBlobs {
		_ = binary.Write(rsrc, binary.LittleEndian, blobOffsets[i]) // OffsetToData (RVA relative to .rsrc symbol)
		_ = binary.Write(rsrc, binary.LittleEndian, uint32(len(b))) // Size
		_ = binary.Write(rsrc, binary.LittleEndian, uint32(0))      // CodePage
		_ = binary.Write(rsrc, binary.LittleEndian, uint32(0))      // Reserved
	}

	// Pad to rawDataStartOff
	for rsrc.Len() < rawDataStartOff {
		rsrc.WriteByte(0)
	}

	// Write raw data blobs with 8-byte alignment
	for _, b := range rawBlobs {
		rsrc.Write(b)
		for rsrc.Len()%8 != 0 {
			rsrc.WriteByte(0)
		}
	}

	rsrcBytes := rsrc.Bytes()

	// Build COFF file:
	// [COFF File Header: 20 bytes]
	// [Section Header (.rsrc): 40 bytes]
	// [.rsrc Section Data: len(rsrcBytes)]
	// [COFF Relocations: totalRes * 10 bytes]
	// [COFF Symbol Table: 1 symbol * 18 bytes]
	// [COFF String Table: 4 bytes]
	const (
		IMAGE_FILE_MACHINE_AMD64 = 0x8664
		CNT_INITIALIZED_DATA     = 0x00000040
		MEM_READ                 = 0x40000000
		IMAGE_REL_AMD64_ADDR32NB = 0x0003
		IMAGE_SYM_CLASS_STATIC   = 3
	)

	secDataFileOff := uint32(20 + 40)
	relocFileOff := secDataFileOff + uint32(len(rsrcBytes))
	symTabFileOff := relocFileOff + uint32(totalRes*10)

	coff := bytes.NewBuffer(nil)

	// 1. COFF File Header (20 bytes)
	_ = binary.Write(coff, binary.LittleEndian, uint16(IMAGE_FILE_MACHINE_AMD64))
	_ = binary.Write(coff, binary.LittleEndian, uint16(1))             // NumberOfSections
	_ = binary.Write(coff, binary.LittleEndian, uint32(0))             // TimeDateStamp
	_ = binary.Write(coff, binary.LittleEndian, symTabFileOff)         // PointerToSymbolTable
	_ = binary.Write(coff, binary.LittleEndian, uint32(1))             // NumberOfSymbols
	_ = binary.Write(coff, binary.LittleEndian, uint16(0))             // SizeOfOptionalHeader
	_ = binary.Write(coff, binary.LittleEndian, uint16(0))             // Characteristics

	// 2. Section Header ".rsrc" (40 bytes)
	var secName [8]byte
	copy(secName[:], ".rsrc")
	coff.Write(secName[:])
	_ = binary.Write(coff, binary.LittleEndian, uint32(0))                  // VirtualSize
	_ = binary.Write(coff, binary.LittleEndian, uint32(0))                  // VirtualAddress
	_ = binary.Write(coff, binary.LittleEndian, uint32(len(rsrcBytes)))     // SizeOfRawData
	_ = binary.Write(coff, binary.LittleEndian, secDataFileOff)             // PointerToRawData
	_ = binary.Write(coff, binary.LittleEndian, relocFileOff)               // PointerToRelocations
	_ = binary.Write(coff, binary.LittleEndian, uint32(0))                  // PointerToLinenumbers
	_ = binary.Write(coff, binary.LittleEndian, uint16(totalRes))           // NumberOfRelocations
	_ = binary.Write(coff, binary.LittleEndian, uint16(0))                  // NumberOfLinenumbers
	_ = binary.Write(coff, binary.LittleEndian, uint32(CNT_INITIALIZED_DATA|MEM_READ))

	// 3. Section Raw Data
	coff.Write(rsrcBytes)

	// 4. Relocations (10 bytes each: VirtualAddress(4), SymbolTableIndex(4), Type(2))
	for i := 0; i < totalRes; i++ {
		rva := uint32(dataEntriesOff + i*16)
		_ = binary.Write(coff, binary.LittleEndian, rva)
		_ = binary.Write(coff, binary.LittleEndian, uint32(0)) // symbol 0 (.rsrc)
		_ = binary.Write(coff, binary.LittleEndian, uint16(IMAGE_REL_AMD64_ADDR32NB))
	}

	// 5. Symbol Table (1 symbol, 18 bytes)
	coff.Write(secName[:])                                        // Name ".rsrc"
	_ = binary.Write(coff, binary.LittleEndian, uint32(0))        // Value
	_ = binary.Write(coff, binary.LittleEndian, int16(1))         // SectionNumber (1)
	_ = binary.Write(coff, binary.LittleEndian, uint16(0))        // Type
	_ = binary.Write(coff, binary.LittleEndian, uint8(IMAGE_SYM_CLASS_STATIC))
	_ = binary.Write(coff, binary.LittleEndian, uint8(0))         // NumberOfAuxSymbols

	// 6. String Table (empty = 4-byte length of 4)
	_ = binary.Write(coff, binary.LittleEndian, uint32(4))

	return coff.Bytes(), nil
}

func align8(n int) int {
	return (n + 7) &^ 7
}
