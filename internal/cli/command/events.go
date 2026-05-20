package command

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// eventEnvelope mirrors the SSE payload emitted by EventHandler.serveBusFiltered.
type eventEnvelope struct {
	Topic   string          `json:"topic"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	TS      string          `json:"ts"`
}

func newEventsCmd() *cobra.Command {
	var (
		appRef  string
		noWatch bool
	)
	c := &cobra.Command{
		Use:   "events",
		Short: "Tail the event bus (deploy, app, domain events)",
		Long: "Subscribe to the server's SSE event bus. By default the stream is " +
			"global. Use --app to filter to a single app. Use --no-watch to print " +
			"the first event that arrives and exit.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			path := "/api/v1/events"
			if appRef != "" {
				a, err := resolveApp(c, cmd.Context(), appRef)
				if err != nil {
					return err
				}
				path = "/api/v1/apps/" + a.ID + "/events"
			}
			ch, cancel, err := c.SSE(cmd.Context(), path, "")
			if err != nil {
				return err
			}
			defer cancel()
			for ev := range ch {
				var env eventEnvelope
				if json.Unmarshal([]byte(ev.Data), &env) != nil {
					// Not a structured envelope — print raw and continue.
					fmt.Fprintln(cmd.OutOrStdout(), ev.Data)
					if noWatch {
						return nil
					}
					continue
				}
				ts := env.TS
				if ts == "" {
					ts = time.Now().UTC().Format(time.RFC3339)
				}
				if len(env.Payload) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %-25s %s  %s\n",
						ts, env.Topic, env.Type, string(env.Payload))
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %-25s %s\n",
						ts, env.Topic, env.Type)
				}
				if noWatch {
					return nil
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&appRef, "app", "", "Filter events to a single app (name or id)")
	c.Flags().BoolVar(&noWatch, "no-watch", false, "Print the first event and exit")
	return c
}
