package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
)

type getContentListCmd struct {
	Category string `flag:"category" usage:"show only this {category} id (e.g. MY-C0002; filtered client-side, the wire request takes none)"`
}

var getContentListCommand = &command{
	name: "get-content-list", needTVFile: true,
	summary: "dump the TV's content list as TSV, all categories",
	cmd:     new(getContentListCmd),
}

// run prints a TSV header and row per item. Category filtering is client-side.
func (c *getContentListCmd) run(ctx context.Context, g *globals, _ []string) error {
	return g.withTV(ctx, func(_ tvData, cl *artClient) error {
		items, err := cl.ContentList()
		if err != nil {
			return err
		}
		w := bufio.NewWriter(os.Stdout)
		fmt.Fprintln(w, "content_id\tcategory_id\tcontent_type\timage_date")
		for _, it := range items {
			if c.Category != "" && it.CategoryID != c.Category {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", it.ContentID, it.CategoryID, it.ContentType, it.ImageDate)
		}
		return w.Flush()
	})
}
