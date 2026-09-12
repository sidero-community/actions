// talos2disk installs Talos Linux from the Image Factory onto the machine it
// runs on: it selects the disk, registers the schematic, writes the image,
// stamps kernel arguments, writes network configuration to META and records
// the result on the Hardware object.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Info("TALOS2DISK - Install Talos Linux from the Image Factory")

	in, err := inputsFromEnv()
	if err != nil {
		logger.Error(err.Error())

		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, in, defaultDeps(logger)); err != nil {
		logger.Error(err.Error())

		return 1
	}

	return 0
}
