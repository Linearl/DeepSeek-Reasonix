package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"reasonix/internal/eval/replay"
)

func main() {
	input := flag.String("input", "", "path to a live paired-run dataset")
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "-input is required")
		os.Exit(2)
	}
	dataset, err := replay.LoadReleaseDataset(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	report, err := replay.EvaluateReleaseGate(dataset)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
	if !report.Pass {
		os.Exit(1)
	}
}
