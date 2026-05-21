package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/donutnomad/wlang/wflang"
)

func main() {
	jsonMode := flag.Bool("json", false, "format as stable JSON instead of pseudocode")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: wlfmt [-json] [file]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	src, err := readInput(flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "wlfmt:", err)
		os.Exit(1)
	}

	var out []byte
	if *jsonMode {
		out, err = wflang.FormatJSON(src)
	} else {
		out, err = wflang.FormatPseudoCode(src)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wlfmt:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintln(os.Stderr, "wlfmt:", err)
		os.Exit(1)
	}
	if len(out) == 0 || out[len(out)-1] != '\n' {
		fmt.Fprintln(os.Stdout)
	}
}

func readInput(args []string) ([]byte, error) {
	switch len(args) {
	case 0:
		return io.ReadAll(os.Stdin)
	case 1:
		return os.ReadFile(args[0])
	default:
		return nil, fmt.Errorf("expected at most one file, got %d", len(args))
	}
}
