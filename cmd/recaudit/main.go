package main

import (
	"fmt"
	"os"

	"tg-recs-audit/internal/app"
)

func main() {
	opts, err := app.ParseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := app.Run(opts, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
