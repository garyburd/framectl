package main

import (
	"context"
	"fmt"
	"os"
)

var sendImageCommand = &command{
	name: "send-image", needTVFile: true, minArgs: 1, args: "<image>...",
	summary: "upload each image to My Photos, printing its content id (- reads paths from stdin)",
	cmd:     runnerFunc(runSendImage),
}

// runSendImage uploads each file and prints its content id. It creates no
// managed records, so a later sync treats the uploads as unmanaged.
func runSendImage(ctx context.Context, g *globals, paths []string) error {
	paths, err := expandStdin(paths)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no images to send (give files, or - to read paths from stdin)")
	}

	// Check everything up front so a bad path fails before dialing the TV.
	for _, path := range paths {
		if !isImage(path) {
			return fmt.Errorf("%s is not a supported image (.jpg/.jpeg/.png)", path)
		}
		if _, err := os.Stat(path); err != nil {
			return err
		}
	}

	return g.withTV(ctx, func(_ tvData, cl *artClient) error {
		for _, path := range paths {
			id, err := cl.UploadPhoto(ctx, path)
			if err != nil {
				return fmt.Errorf("send %s: %w", path, err)
			}
			fmt.Println(id)
		}
		return nil
	})
}
