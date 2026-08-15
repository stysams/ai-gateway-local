// Command gateway is the headless ai-gateway binary.
package main

import (
	"os"

	"ai-gateway/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}
