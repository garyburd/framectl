package main

import (
	"context"
	"fmt"
)

var deleteImageListCommand = &command{
	name: "delete-image-list", needTVFile: true, minArgs: 1, args: "<content-id>...",
	summary: "delete photos from the TV by content id (- reads ids from stdin)",
	cmd:     runnerFunc(runDeleteImageList),
}

// runDeleteImageList sends one request and leaves sync's managed records alone.
func runDeleteImageList(ctx context.Context, g *globals, ids []string) error {
	ids, err := expandStdin(ids)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("no content ids to delete (give ids, or - to read them from stdin)")
	}
	return g.withTV(ctx, func(_ tvData, cl *artClient) error {
		if err := cl.DeletePhotos(ids); err != nil {
			return err
		}
		for _, id := range ids {
			fmt.Printf("deleted %s\n", id)
		}
		return nil
	})
}
