package main

import (
	"bufio"
	"fmt"
	"gitlab.com/gomidi/midi"
	"gitlab.com/gomidi/midi/reader"
	"gitlab.com/gomidi/midi/writer"
	"gitlab.com/gomidi/rtmididrv"
	"net"
	"strings"
	"sync"
	"time"
)

type vmixClient struct {
	conn net.Conn
	w    *bufio.Writer
	r    *bufio.Reader
	sync.Mutex
	connected bool
}

func NewClient() *vmixClient {
	return &vmixClient{}
}

func (c *vmixClient) Connect(apiAddress string) error {
	c.connected = false
	for c.connected == false {
		conn, err := net.Dial("tcp", apiAddress)

		if err == nil {
			c.conn = conn
			c.w = bufio.NewWriter(conn)
			c.r = bufio.NewReader(conn)
			c.connected = true
		} else if strings.Contains(err.Error(), "connection refused") {
			fmt.Println("vmix api is inaccessible.  Probably because vmix is not running, or wrong API IP address")
			fmt.Println("Waiting 5 seconds and trying again")
			c.connected = false
			time.Sleep(5)
		} else {
			fmt.Println("Unable to connect. Error was: ", err)
			return err
		}
	}
	return nil
}

func (c *vmixClient) SendMessage(message string) error {
	c.Lock()
	pub := fmt.Sprintf("%v\r\n", message)
	_, err := c.w.WriteString(pub)
	if err == nil {
		err = c.w.Flush()
	}
	c.Unlock()

	return err
}

func (c *vmixClient) GetMessage(vmixMessage chan string, wg *sync.WaitGroup) {

	// Subscribe to the activator feed in the vMix API
	err := c.SendMessage("SUBSCRIBE ACTS")
	if err != nil {
		fmt.Println("Error in GetMessage.SendMessage: ", err)
		wg.Done()
	}

	//Capture all responses from the vMix API
	for {
		line, err := c.r.ReadString('\n')

		if err == nil {
			vmixMessage <- line
		} else {
			wg.Done()
			fmt.Println("Error in GetMessage.ReadString: ", err)
		}
	}
}

func ProcessMessage(vmixMessage chan string) {
	for {
		mess := <-vmixMessage
		fmt.Println("Received in channel:", mess)
	}
}

func ProcessMidi(midiMessageChan chan []byte) {
	// message is a byte [type button velocity]
	// type 144 is a button push
	// type 176 is a control change
	for {
		msg := <-midiMessageChan
		switch msg[0] {
		case 144:
			if msg[2] == 0 {
				fmt.Println("Button Up:", msg[1])
			}
			if msg[2] == 127 {
				fmt.Println("Button Down:", msg[1])
			}
		case 176:
			fmt.Println("Control change. Fader:", msg[1], "Value:", msg[2])
		}
	}
}

func ListenMidi(inPort midi.In, midiMessageChan chan []byte, wg *sync.WaitGroup) {
	//Listen to midi port, push any messages to the midiMessage channel

	rd := reader.New(
		reader.NoLogger(),

		// Fetch every message
		reader.Each(func(pos *reader.Position, msg midi.Message) {
			midiMessageChan <- msg.Raw()
		}),
	)

	err := rd.ListenTo(inPort)
	if err == nil {
		wg.Wait()
	} else {
		fmt.Println(err)
		wg.Done()
	}
}

func setAPCLED(outPort midi.Out, button uint8, color string) {
	values := map[string]uint8{
		"green":       1,
		"greenBlink":  2,
		"red":         3,
		"redBlink":    4,
		"yellow":      5,
		"yellowBlink": 6,
		"on":          1, //for round buttons - they can only be red(on), or red blinking (blink)
		"blink":       2,
	}
	wr := writer.New(outPort)
	wr.ConsolidateNotes(false)
	if color == "off" {
		_ = writer.NoteOff(wr, button)
	} else {
		_ = writer.NoteOn(wr, button, values[color])
	}

}

func getMIDIPorts() (midi.In, midi.Out) {
	//Iterate through all midi ports on the driver and identify the ones
	//belonging to an APC Mini.  Return the input port and output port.

	foundAPCIn := false
	foundAPCOut := false

	drv, err := rtmididrv.New()
	if err != nil {
		panic(err)
	}

	inPorts, _ := drv.Ins()
	outPorts, _ := drv.Outs()

	inPort, err := midi.OpenIn(drv, 0, "")
	if err != nil {
		panic("No MIDI Input Ports found")
		return nil, nil
	}

	outPort, err := midi.OpenOut(drv, 0, "")
	if err != nil {
		panic("No MIDI Output Ports found")
		return nil, nil
	}

	for i, port := range inPorts {
		if strings.Contains(port.String(), "APC MINI") {
			inPort, err = midi.OpenIn(drv, i, "")
			if err != nil {
				panic("Unable to open MIDI port")
				return nil, nil
			} else {
				foundAPCIn = true
			}
		}
	}

	for i, port := range outPorts {
		if strings.Contains(port.String(), "APC MINI") {
			outPort, err = midi.OpenOut(drv, i, "")
			if err != nil {
				panic("Unable to open MIDI port")
				return nil, nil
			} else {
				foundAPCOut = true
			}
		}
	}

	if foundAPCIn && foundAPCOut {
		return inPort, outPort
	} else {
		panic("No APC Mini found. Aborting")
		return nil, nil
	}
}

func main() {
	var wg sync.WaitGroup
	wg.Add(2)

	vmixClient := NewClient()
	err := vmixClient.Connect("192.168.1.173:8099")
	if err != nil {
		fmt.Println("Error connecting to vmix API:")
		panic(err)
	}
	vmixMessageChan := make(chan string)
	defer close(vmixMessageChan)

	go vmixClient.GetMessage(vmixMessageChan, &wg)
	go ProcessMessage(vmixMessageChan)

	midiMessageChan := make(chan []byte, 100)
	defer close(midiMessageChan)

	inPort, outPort := getMIDIPorts()
	fmt.Println(outPort)
	setAPCLED(outPort, 71, "off")

	go ListenMidi(inPort, midiMessageChan, &wg)
	go ProcessMidi(midiMessageChan)

	wg.Wait()

}
