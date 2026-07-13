package main

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
)

type setSlideshowStatusCmd struct {
	Shuffle  bool   `flag:"shuffle" usage:"show images in random order"`
	Category string `flag:"category" usage:"{category} to run the slideshow over (MY-C0002 = My Photos, MY-C0008 = store)"`
}

var setSlideshowStatusCommand = &command{
	name: "set-slideshow-status", needTVFile: true, minArgs: 1, args: "<off|minutes>",
	summary: "start or stop the firmware slideshow (minutes: " + allowedSlideshowValues() + ")",
	// Constant defaults use the field value rather than a tag.
	cmd: &setSlideshowStatusCmd{Category: uploadsCategory},
}

// run validates before dialing.
func (c *setSlideshowStatusCmd) run(ctx context.Context, g *globals, args []string) error {
	value := args[0]
	if !slices.Contains(slideshowValues, value) {
		return fmt.Errorf("invalid value %q (use \"off\" or minutes: %s)", value, allowedSlideshowValues())
	}
	return g.withTV(ctx, func(_ tvData, cl *artClient) error {
		if err := cl.SendSlideshow(newSlideshow(c.Category, value, c.Shuffle)); err != nil {
			return err
		}
		if value == "off" {
			log.Println("slideshow: turned off")
		} else {
			order := "in order"
			if c.Shuffle {
				order = "shuffled"
			}
			log.Printf("slideshow: changing every %s minutes, %s, over %s", value, order, c.Category)
		}
		return nil
	})
}

// slideshow holds the art-mode slideshow settings.
type slideshow struct {
	Value      string // one of slideshowValues
	CategoryID string
	Type       string // "slideshow" (ordered) or "shuffleslideshow"
}

// slideshowValues lists the supported intervals.
var slideshowValues = []string{"off", "3", "15", "60", "720", "1440"}

// newSlideshow builds settings for an allowed value.
func newSlideshow(categoryID, value string, shuffle bool) slideshow {
	ss := slideshow{Value: value, CategoryID: categoryID, Type: "slideshow"}
	if shuffle {
		ss.Type = "shuffleslideshow"
	}
	return ss
}

// allowedSlideshowValues formats accepted minutes for help and errors.
func allowedSlideshowValues() string {
	var vals []string
	for _, v := range slideshowValues {
		if v != "off" {
			vals = append(vals, v)
		}
	}
	return strings.Join(vals, ", ")
}
