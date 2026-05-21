package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/wflang"
)

func main() {
	funcName := flag.String("func", "", "top-level Go function to translate")
	lang := flag.String("lang", "wflang/v1", "wlang language version")
	pseudo := flag.Bool("pseudo", false, "print pseudocode instead of JSON")
	manifest := flag.String("manifest", "", "write package import manifest JSON to this path")
	embedImportMap := flag.Bool("embed-import-map", false, "embed package import map in JSON output")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: go2wl -func Name [-pseudo] [-manifest path] [-embed-import-map] [file]\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *funcName == "" {
		fmt.Fprintln(os.Stderr, "go2wl: -func is required")
		os.Exit(1)
	}
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "go2wl: expected exactly one Go source file")
		os.Exit(1)
	}
	result, err := go2wlang.TranslateFilePathDetailed(flag.Arg(0), go2wlang.Options{
		FuncName:       *funcName,
		Lang:           *lang,
		EmbedImportMap: *embedImportMap,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "go2wl:", err)
		os.Exit(1)
	}
	if *manifest != "" {
		raw, err := json.MarshalIndent(result.Imports, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "go2wl:", err)
			os.Exit(1)
		}
		raw = append(raw, '\n')
		if err := os.WriteFile(*manifest, raw, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "go2wl: write manifest %s: %v\n", *manifest, err)
			os.Exit(1)
		}
	}
	out := result.JSON
	if *pseudo {
		out, err = wflang.FormatPseudoCode(out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "go2wl:", err)
			os.Exit(1)
		}
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintln(os.Stderr, "go2wl:", err)
		os.Exit(1)
	}
	if len(out) == 0 || out[len(out)-1] != '\n' {
		fmt.Fprintln(os.Stdout)
	}
}
