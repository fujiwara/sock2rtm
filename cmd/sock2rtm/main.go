package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/fujiwara/sock2rtm"
	"github.com/hashicorp/logutils"
)

var version = "current"

func main() {
	var port int
	var showVersion bool
	flag.IntVar(&port, "port", 8888, "port number")
	flag.BoolVar(&sock2rtm.Debug, "debug", false, "debug mode")
	flag.BoolVar(&showVersion, "version", false, "show version")
	flag.VisitAll(envToFlag)
	flag.Parse()
	if showVersion {
		log.Println("sock2rtm", version)
		os.Exit(0)
	}

	filter := &logutils.LevelFilter{
		Levels:   []logutils.LogLevel{"debug", "info", "warn", "error"},
		Writer:   os.Stderr,
		MinLevel: logutils.LogLevel("info"),
	}
	if sock2rtm.Debug {
		filter.MinLevel = logutils.LogLevel("debug")
	}
	log.SetOutput(filter)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := sock2rtm.New(port)
	if err != nil {
		panic(err)
	}
	app.Run(ctx)
}

func envToFlag(f *flag.Flag) {
	names := []string{
		strings.ToUpper(strings.Replace(f.Name, "-", "_", -1)),
		strings.ToLower(strings.Replace(f.Name, "-", "_", -1)),
	}
	for _, name := range names {
		if s := os.Getenv(name); s != "" {
			f.Value.Set(s)
			break
		}
	}
}
