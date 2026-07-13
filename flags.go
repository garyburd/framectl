package main

import (
	"context"
	"flag"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// runner is implemented by a tagged flag struct or runnerFunc.
type runner interface {
	run(ctx context.Context, g *globals, args []string) error
}

// runnerFunc adapts a function to runner, for commands with no flags.
type runnerFunc func(context.Context, *globals, []string) error

func (f runnerFunc) run(ctx context.Context, g *globals, args []string) error {
	return f(ctx, g, args)
}

// placeholderReplacer produces flag.UnquoteUsage placeholders from {word}.
var placeholderReplacer = strings.NewReplacer("{", "`", "}", "`")

// registerFlags registers tagged fields from cmd:
//
//	Host string `flag:"host" default:"127.0.0.1" usage:"server {hostname}"`
//
// Untagged fields and non-struct runners are skipped. Defaults come from the
// tag or current field value. {word} supplies a usage placeholder. Invalid
// tags panic and are caught when generated help visits every command.
func registerFlags(fs *flag.FlagSet, cmd runner) {
	v := reflect.ValueOf(cmd)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return
	}
	v = v.Elem()
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		name, ok := f.Tag.Lookup("flag")
		if !ok {
			continue
		}
		usage := placeholderReplacer.Replace(f.Tag.Get("usage"))
		def, hasDef := f.Tag.Lookup("default")
		switch p := v.Field(i).Addr().Interface().(type) {
		case *string:
			if hasDef {
				*p = def
			}
			fs.StringVar(p, name, *p, usage)
		case *bool:
			if hasDef {
				b, err := strconv.ParseBool(def)
				if err != nil {
					panic(fmt.Sprintf("%s.%s: bad default %q: %v", t.Name(), f.Name, def, err))
				}
				*p = b
			}
			fs.BoolVar(p, name, *p, usage)
		case *time.Duration:
			if hasDef {
				d, err := time.ParseDuration(def)
				if err != nil {
					panic(fmt.Sprintf("%s.%s: bad default %q: %v", t.Name(), f.Name, def, err))
				}
				*p = d
			}
			fs.DurationVar(p, name, *p, usage)
		default:
			panic(fmt.Sprintf("%s.%s: unsupported flag field type %s", t.Name(), f.Name, f.Type))
		}
	}
}
