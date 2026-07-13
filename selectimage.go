package main

import (
	"context"
	"log"
)

type selectImageCmd struct {
	Category string `flag:"category" usage:"{category} id of the image (optional; the TV infers it when omitted)"`
	Show     bool   `flag:"show" default:"true" usage:"display the photo now; -show=false sets the selection only"`
}

var selectImageCommand = &command{
	name: "select-image", needTVFile: true, minArgs: 1, args: "<content-id>",
	summary: "select a photo by content id and display it (-show=false only sets the selection)",
	cmd:     new(selectImageCmd),
}

// Show displays immediately; false only sets the selection.
func (c *selectImageCmd) run(ctx context.Context, g *globals, args []string) error {
	return g.withTV(ctx, func(_ tvData, cl *artClient) error {
		id := args[0]
		if err := cl.SelectImage(id, c.Category, c.Show); err != nil {
			return err
		}
		if c.Show {
			log.Printf("displaying %s", id)
		} else {
			log.Printf("set selection to %s (show:false)", id)
		}
		return nil
	})
}
