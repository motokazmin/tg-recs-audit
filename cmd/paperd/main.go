// paperd — проверка идей вперёд на виртуальных деньгах с админкой (ADR 0005).
package main

import (
	"log"
	"os"

	"tg-recs-audit/internal/app"
)

func main() {
	o, err := app.ParsePaperFlags(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if err := app.RunPaper(o); err != nil {
		log.Fatal(err)
	}
}
