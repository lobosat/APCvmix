package main

import (
	"fmt"
	"gitlab.com/gomidi/midi"
	"gitlab.com/gomidi/midi/reader"

	"gitlab.com/gomidi/midi/writer"
	// replace with e.g. "gitlab.com/gomidi/rtmididrv" for real midi connections
	"gitlab.com/gomidi/rtmididrv"
)

// This example reads from the first input and and writes to the first output port
func main() {
	// you would take a real driver here e.g. rtmididrv.New()
	drv, err := rtmididrv.New()

	// make sure to close all open ports at the end
	defer drv.Close()

	ins, err := drv.Ins()
	must(err)

	outs, err := drv.Outs()
	must(err)

	in, out := ins[1], outs[1]

	must(in.Open())
	must(out.Open())

	defer in.Close()
	defer out.Close()

	// the writer we are writing to
	wr := writer.New(out)

	// to disable logging, pass mid.NoLogger() as option
	rd := reader.New(
		reader.NoLogger(),
		// write every message to the out port
		reader.Each(func(pos *reader.Position, msg midi.Message) {
			fmt.Printf("got %s\n", msg)
		}),
	)

	// listen for MIDI
	err = rd.ListenTo(in)
	must(err)

	err = writer.NoteOn(wr, 1, 3)
	must(err)

	//time.Sleep(100 * time.Millisecond)
	//err = writer.NoteOff(wr, 60)
	//
	//must(err)
	// Output: got channel.NoteOn channel 0 key 60 velocity 100
	// got channel.NoteOff channel 0 key 60
}

func must(err error) {
	if err != nil {
		panic(err.Error())
	}
}

/* import (
	"fmt"
	"gitlab.com/gomidi/midi"
	"gitlab.com/gomidi/midi/reader"
	"strings"
	"gitlab.com/gomidi/rtmididrv"
)


// This example reads from the first input port
func main() {
	drv, err := rtmididrv.New()
	defer drv.Close()

	if err != nil {
		panic(err)
	}

	ins, err := drv.Ins()
	outs, err := drv.Outs()



	rd := reader.New(
		reader.NoLogger(),
		// write every message to the out port
		reader.Each(func(pos *reader.Position, msg midi.Message) {
			fmt.Printf("got %s\n", msg)
		}),
	)

	for i := range ins {
		if strings.Contains(ins[i].String(), "APC") {
			in := ins[i]
			for {
				err = rd.ListenTo(in)
				if err != nil {
					panic(err)
				}
			}

		}
	}

	for i := range outs {
		if strings.Contains(outs[i].String(), "APC") {

		}
	}


}
*/
