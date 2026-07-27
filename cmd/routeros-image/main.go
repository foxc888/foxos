package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/foxc888/foxos/internal/routerosimage"
)

func main() {
	input := flag.String("input", "", "Docker image archive to flatten")
	output := flag.String("output", "", "RouterOS-compatible image archive to write")
	architecture := flag.String("architecture", "amd64", "required Linux image architecture")
	flag.Parse()
	metadata, err := routerosimage.Flatten(*input, *output, *architecture)
	if err != nil {
		fmt.Fprintf(os.Stderr, "routeros image error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("flattened %d layer(s) for linux/%s into %s\n", metadata.OriginalLayers, metadata.Architecture, *output)
}
