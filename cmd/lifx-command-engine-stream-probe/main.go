// lifx-command-engine-stream-probe replays a finalized WAV in real time while
// running bounded speculative Whisper passes. It is an experimental benchmark,
// not a public streaming protocol or microphone implementation.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/speech/whispercpp"
	"github.com/alessio-palumbo/lifx-command-engine/internal/streamprobe"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	set := flag.NewFlagSet("lifx-command-engine-stream-probe", flag.ContinueOnError)
	audioPath := set.String("audio", "", "mono 16-bit PCM WAV to replay")
	expected := set.String("expected", "", "optional expected transcript")
	language := set.String("language", "en", "spoken language")
	whisperCommand := set.String("whisper-command", "", "whisper-server executable")
	whisperModel := set.String("whisper-model", "", "local whisper.cpp model")
	chunkDuration := set.Duration("chunk", 100*time.Millisecond, "simulated capture chunk duration")
	partialAfter := set.Duration("partial-after", time.Second, "audio required before the first speculative pass")
	partialInterval := set.Duration("partial-interval", 1500*time.Millisecond, "minimum audio interval between speculative passes")
	partialWindow := set.Duration("partial-window", 4*time.Second, "maximum trailing audio used by a speculative pass")
	maxUtterance := set.Duration("max-utterance", 15*time.Second, "maximum complete utterance accepted for finalization")
	startupTimeout := set.Duration("startup-timeout", 2*time.Minute, "whisper-server model readiness timeout")
	overallTimeout := set.Duration("timeout", 5*time.Minute, "overall probe timeout")
	var whisperArgs stringList
	set.Var(&whisperArgs, "whisper-arg", "whisper-server startup argument (repeatable)")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", set.Args())
	}
	if *audioPath == "" || *whisperCommand == "" || *whisperModel == "" {
		return fmt.Errorf("-audio, -whisper-command and -whisper-model are required")
	}

	transcriber, err := whispercpp.NewPersistentTranscriber(*whisperCommand, *whisperModel, whisperArgs)
	if err != nil {
		return err
	}
	defer transcriber.Close()
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), *startupTimeout)
	err = transcriber.Start(startupCtx)
	cancelStartup()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *overallTimeout)
	defer cancel()
	report, err := streamprobe.Run(ctx, transcriber, streamprobe.Config{
		AudioPath: *audioPath, Language: *language, ExpectedText: *expected,
		ChunkDuration: *chunkDuration, PartialAfter: *partialAfter, PartialInterval: *partialInterval,
		PartialWindow: *partialWindow, MaxUtterance: *maxUtterance,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

type stringList []string

func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}
