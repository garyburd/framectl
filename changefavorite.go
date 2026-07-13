package main

import (
	"context"
	"fmt"
	"log"
)

var changeFavoriteCommand = &command{
	name: "change-favorite", needTVFile: true, minArgs: 2, args: "<content-id> <on|off>",
	summary: "mark a store artwork as a favorite (on), or unmark it (off); the TV rejects uploaded photos",
	cmd:     runnerFunc(runChangeFavorite),
}

func runChangeFavorite(ctx context.Context, g *globals, args []string) error {
	id, status := args[0], args[1]
	if status != "on" && status != "off" {
		return fmt.Errorf("favorite status must be on or off, not %q: %w", status, errUsage)
	}
	return g.withTV(ctx, func(_ tvData, cl *artClient) error {
		if err := cl.ChangeFavorite(id, status); err != nil {
			return err
		}
		log.Printf("favorite %s: %s", status, id)
		return nil
	})
}
