// Command anti is a scratchpad for Apex, after Antinote: a window for the
// notes you are going to throw away.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	apexapi "github.com/mariusae/apex/go/apex"
)

const helpText = `anti is a scratchpad for the notes you are going to throw away.

Usage:
  anti [-new] [-dir DIR]

Run it in an Apex session (B2 anti in a tag) for a window on the newest
note. What is typed is kept as it is typed; the notes are plain files in
DIR, ~/.anti unless -dir or $ANTI say otherwise. A second anti brings the
first one's window forward, with -new on a new note.

In the window:
  Prev, Next       the note before, the note after; past the newest, a new one
  New              a new note. A note left with nothing in it goes
  Front, Promote   the newest note; this note made the newest
  Void, Restore    throw this note into DIR/void; bring back the last thrown
  Export [DEST]    write the note out for keeping, and open it: into DEST, a
                   file or directory, else anti.export, else ~/Documents
  Check            tick the item the cursor is on; B3 on a box ticks it too

The first line of a note can make it something more:
  math             a line ending in = is worked out, the answer after the =;
                   name : value names a value; ans is the last answer;
                   10 ft to m, 3 cups in ml, 72 F to C convert
  list             each line an item: [ ] buy milk; end a line in /x to tick it
  sum, avg, count  the label says what the note adds up to
  code             nothing more; it is kept as written
"math: Budget" titles the note. A line beginning // is a comment, left out.

In any note, a finished line (one with a newline after it) can be a command:
  timer            a stopwatch; timer 5 counts down five minutes, timer 3:30,
                   timer 9am and timer 21:15 too; timer 25 5 is a pomodoro,
                   timer pomo the usual one; timer p, r, s pause, restart, stop
  paste            from now on what is copied is added to the note, a line each;
                   paste(, ) puts ", " between; paste again stops it

Settings (apex set KEY VALUE):
  anti.expire      how long a note left alone stays before it goes to the
                   void: 14d unless this says, 0 for ever
  anti.checked     what a ticked item does: stay, bottom, or delete
  anti.export      where Export writes without a DEST
`

func main() {
	fs := flag.NewFlagSet("anti", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, helpText) }
	fresh := fs.Bool("new", false, "start on a new note")
	dir := fs.String("dir", "", "where the notes are")
	help := fs.Bool("help", false, "")
	_ = fs.Parse(os.Args[1:])
	if *help {
		fmt.Print(helpText)
		return
	}
	if fs.NArg() > 0 {
		fs.Usage()
		os.Exit(2)
	}
	if err := run(notesDir(*dir), *fresh); err != nil {
		fmt.Fprintf(os.Stderr, "anti: %v\n", err)
		os.Exit(1)
	}
}

// notesDir is -dir, else $ANTI, else ~/.anti.
func notesDir(flagged string) string {
	d := flagged
	if d == "" {
		d = os.Getenv("ANTI")
	}
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".anti")
	}
	if abs, err := filepath.Abs(d); err == nil {
		d = abs
	}
	return filepath.Clean(d)
}

func run(dir string, fresh bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tool, err := apexapi.Attach("anti", nil)
	if err != nil {
		return err
	}
	defer tool.Close()

	// one anti a window: one already serving it is asked instead, and a
	// window left by an anti that went is taken up again
	var w *apexapi.Window
	if windows, err := tool.Windows(); err == nil {
		for _, info := range windows {
			if !ours(info, dir) {
				continue
			}
			if info.Live {
				other := tool.Window(info.ID)
				if fresh {
					return other.Exec("New")
				}
				return other.Show(0)
			}
			w = tool.Window(info.ID)
		}
	}
	if w == nil {
		if w, err = tool.NewScratch(dir+"/", "anti"); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		<-signals
		cancel()
	}()
	return serve(ctx, tool, w, store{dir: dir}, fresh)
}

// ours says a window is an anti window on dir: one of its notes, labelled.
func ours(info apexapi.WindowInfo, dir string) bool {
	return strings.HasPrefix(info.Path, dir+string(filepath.Separator)) && info.Label != nil && strings.HasPrefix(*info.Label, "anti")
}
