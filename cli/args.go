package main

import (
	"fmt"
	"strconv"
	"strings"
)

type parsedArgs struct {
	Text                string
	Mode                string
	Priority            string
	Dev                 bool
	InjectFile          string
	DiscordHistoryLimit int
}

var validPriorities = map[string]bool{"low": true, "normal": true, "high": true, "critical": true}

// parseArgs parses os.Args[1:] into a parsedArgs. Supported forms:
//
//	soulman "<text>"                      -> Mode: stimulus
//	soulman note "<text>"                  -> Mode: note
//	soulman [--priority P] [--dev] ...     -> flags may appear anywhere
//
// A hand-rolled parser (not the stdlib flag package) because flag doesn't
// cleanly support flags interleaved with a "note" subcommand followed by
// free-form positional text.
func parseArgs(args []string) (parsedArgs, error) {
	res := parsedArgs{Priority: "normal", DiscordHistoryLimit: 20}
	var positional []string
	endOfFlags := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endOfFlags && a == "--" {
			endOfFlags = true
			continue
		}
		switch {
		case !endOfFlags && a == "--dev":
			res.Dev = true
		case !endOfFlags && a == "--priority":
			if i+1 >= len(args) {
				return parsedArgs{}, fmt.Errorf("--priority requires a value")
			}
			i++
			res.Priority = args[i]
		case !endOfFlags && strings.HasPrefix(a, "--priority="):
			res.Priority = strings.TrimPrefix(a, "--priority=")
		case !endOfFlags && a == "--limit":
			if i+1 >= len(args) {
				return parsedArgs{}, fmt.Errorf("--limit requires a value")
			}
			i++
			n, convErr := strconv.Atoi(args[i])
			if convErr != nil {
				return parsedArgs{}, fmt.Errorf("--limit must be a number, got %q", args[i])
			}
			res.DiscordHistoryLimit = n
		case !endOfFlags && strings.HasPrefix(a, "--"):
			return parsedArgs{}, fmt.Errorf("unrecognized flag: %s", a)
		default:
			positional = append(positional, a)
		}
	}

	if !validPriorities[res.Priority] {
		return parsedArgs{}, fmt.Errorf("invalid --priority %q: must be one of low, normal, high, critical", res.Priority)
	}

	if len(positional) > 0 && positional[0] == "discord-history" {
		res.Mode = "discord-history"
		return res, nil
	}

	if len(positional) == 0 {
		return parsedArgs{}, fmt.Errorf(`usage: soulman [--dev] [--priority low|normal|high|critical] [note] "<text>"`)
	}

	if positional[0] == "inject" {
		if len(positional) < 2 {
			return parsedArgs{}, fmt.Errorf("usage: soulman inject <file>")
		}
		res.Mode = "inject"
		res.InjectFile = positional[1]
		return res, nil
	}

	res.Mode = "stimulus"
	if positional[0] == "note" {
		res.Mode = "note"
		positional = positional[1:]
	}

	if len(positional) == 0 {
		return parsedArgs{}, fmt.Errorf("missing text argument")
	}
	res.Text = strings.Join(positional, " ")
	if strings.TrimSpace(res.Text) == "" {
		return parsedArgs{}, fmt.Errorf("text argument cannot be empty")
	}

	return res, nil
}
