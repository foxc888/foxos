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
	component := flag.String("component", "", "required component identity: foxos, mihomo, or mosdns")
	flag.Parse()
	if *component == "" {
		fmt.Fprintln(os.Stderr, "routeros image error: -component is required")
		os.Exit(2)
	}
	metadata, err := routerosimage.FlattenComponent(*input, *output, *architecture, *component)
	if err != nil {
		fmt.Fprintf(os.Stderr, "routeros image error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("flattened %d layer(s) for %s linux/%s into %s\n", metadata.OriginalLayers, metadata.Component, metadata.Architecture, *output)
}
