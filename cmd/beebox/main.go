// Command beebox is a flight recorder for Linux computers: it explains why the
// machine crashed, froze or lost power.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/PurpleDefender249/beebox/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := cli.Main(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
