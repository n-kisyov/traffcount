package main

import (
	"fmt"
	"os"
	"traffcount/internal/tray"
)

func main() {
	outPath := "cmd/traffcount/app.ico"
	if len(os.Args) > 1 {
		outPath = os.Args[1]
	}
	ico := tray.GenerateMultiSizeAppICO()
	if err := os.WriteFile(outPath, ico, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Printf("Generated multi-size app icon: %s (%d bytes)\n", outPath, len(ico))
}
