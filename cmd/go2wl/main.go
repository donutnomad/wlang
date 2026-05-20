package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/wflang/wflang/go2wlang"
	"github.com/wflang/wflang/wflang"
)

func main() {
	funcName := flag.String("func", "", "top-level Go function to translate")
	lang := flag.String("lang", "wflang/v1", "wlang language version")
	pseudo := flag.Bool("pseudo", false, "print pseudocode instead of JSON")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: go2wl -func Name [-pseudo] [file]\n")
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
	src, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "go2wl:", err)
		os.Exit(1)
	}
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{
		FuncName: *funcName,
		Lang:     *lang,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "go2wl:", err)
		os.Exit(1)
	}
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
