package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	engine "github.com/alessio-palumbo/lifx-command-engine/client"
)

func main() {
	sidecar := flag.String("engine", "lifx-command-engine", "path to the sidecar binary")
	text := flag.String("text", "turn desk on", "text command to interpret")
	flag.Parse()

	client, err := engine.New(engine.Config{Path: *sidecar, RestartOnCrash: true})
	fatalIf(err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fatalIf(client.Start(ctx))

	// A real host maps this snapshot from its own discovered device state.
	plan, err := client.Interpret(ctx, engine.InterpretInput{
		Text: *text,
		Snapshot: engine.DeviceSnapshot{
			Locations: []engine.NamedRef{},
			Groups:    []engine.NamedRef{{Label: "Office"}},
			Devices: []engine.SnapshotDevice{{
				Serial: "d073d5000001", Label: "Desk", Group: "Office", Location: "Home",
			}},
		},
	})
	fatalIf(err)
	encoded, err := json.MarshalIndent(plan, "", "  ")
	fatalIf(err)
	fmt.Println(string(encoded))
	// Preview and execution deliberately belong to the host and are omitted.
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
