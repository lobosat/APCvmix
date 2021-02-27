package main

import (
	"bufio"
	"fmt"
	"net/url"

	//"github.com/360EntSecGroup-Skylar/excelize/v2"
	"gitlab.com/gomidi/midi"
	"gitlab.com/gomidi/midi/reader"
	"gitlab.com/gomidi/midi/writer"
	"gitlab.com/gomidi/rtmididrv"
	"net"
	"strings"
	"sync"
	"time"
)

type vmixClientType struct {
	conn net.Conn
	w    *bufio.Writer
	r    *bufio.Reader
	sync.Mutex
	connected bool
}
type vmixFunc struct {
	action string
	input  string
	value  string
}

type overlayResponse struct {
	button   string
	input    string
	tbName   string
	response string
}

var vmixStateSingle = map[string]string{
	"Input":        "",
	"InputPreview": "",
	"Overlay1":     "",
	"Overlay2":     "",
	"Overlay3":     "",
	"Overlay4":     "",
}
var vmixStateMultiple = map[string]map[string]bool{
	"InputPlaying":   {},
	"InputBusAAudio": {},
	"InputBusBAudio": {},
}
var vmixClient = new(vmixClientType)
var wg sync.WaitGroup
var vmixMessageChan = make(chan string)
var midiMessageChan = make(chan []byte, 100)
var midiIn midi.In
var midiOut midi.Out
var overlayResponses = make(map[int]*overlayResponse)

func init() {
	//Connect to the vmix API
	err := vmixAPIConnect("192.168.1.173:8099")
	if err != nil {
		fmt.Println("Error connecting to vmix API:")
		panic(err)
	}

	//Get the APC Mini MIDI In and Out ports
	midiIn, midiOut = getMIDIPorts()

	or := new(overlayResponse)
	or.button = "7"
	or.input = "1"
	or.response = "The Rain in Spain \r\n Stays Mainly in the Plain"
	or.tbName = "Response.Text"

	overlayResponses[7] = or
}

// vmixAPIConnect connects to the vMix API. apiAddress is a string
// of the format ipaddress:port.  By default the vMix API is on port 8099.
// If vMix is not up, this function will continue trying to connect, and will
// block until a connection is achieved.
func vmixAPIConnect(apiAddress string) error {
	vmixClient.connected = false
	for vmixClient.connected == false {
		timeout, _ := time.ParseDuration("20s")
		conn, err := net.DialTimeout("tcp", apiAddress, timeout)

		if err == nil {
			vmixClient.conn = conn
			vmixClient.w = bufio.NewWriter(conn)
			vmixClient.r = bufio.NewReader(conn)
			vmixClient.connected = true
		} else if strings.Contains(err.Error(), "connection timed out") {
			fmt.Println("vmix api is inaccessible.  Probably because vMix is not running")
			fmt.Println("Waiting 5 seconds and trying again")
			vmixClient.connected = false
			time.Sleep(5)
		} else {
			fmt.Println("Unable to connect. Error was: ", err)
			return err
		}
	}
	return nil
}

// SendMessage sends a message to the vMix API. It adds the
// /r/n terminator the API expects
func SendMessage(message string) error {
	vmixClient.Lock()
	pub := fmt.Sprintf("%v\r\n", message)
	_, err := vmixClient.w.WriteString(pub)
	if err == nil {
		err = vmixClient.w.Flush()
	}
	vmixClient.Unlock()

	return err
}

// getMessage connects to the vMix API and issues a subscription to activators.
// It then remains listening for any messages from the API server.  Any messages
// received are sent to the vmixMessageChan channel for consumption.  This is a blocking
// function.  The vmixClient must already be connected to the API and available as a global.
func getMessage() {

	// Subscribe to the activator feed in the vMix API
	err := SendMessage("SUBSCRIBE ACTS")
	if err != nil {
		fmt.Println("Error in GetMessage.SendMessage: ", err)
		wg.Done()
	}

	//Capture all responses from the vMix API
	for {
		line, err := vmixClient.r.ReadString('\n')

		if err == nil {
			vmixMessageChan <- line
			fmt.Println(line)
		} else {
			wg.Done()
			fmt.Println("Error in GetMessage.ReadString: ", err)
		}
	}
}

// ProcessVmixMessage listens to the vMix API channel for any messages from the API.
// It uses these messages to update the vMix State maps which are used for the
// conditional actions. This is a blocking function.
func ProcessVmixMessage() {
	for {
		messageSlice := strings.Fields(<-vmixMessageChan)
		// ex:  [ACTS OK InputPlaying 9 1]
		// messageSlice[2] - Action
		// messageSlice[3] - Input
		// messageSlice[4] - Value (usually 0 for off, 1 for on)

		if messageSlice[0] == "ACTS" && messageSlice[1] == "OK" {
			parameter := messageSlice[2]
			input := messageSlice[3]
			state := messageSlice[4]
			switch parameter {
			case "Input", "InputPreview", "Overlay1", "Overlay2", "Overlay3", "Overlay4":
				if state == "1" {
					vmixStateSingle[parameter] = input
				}
			case "InputPlaying", "InputBusAAudio", "InputBusBAudio":
				if state == "0" {
					vmixStateMultiple[parameter][input] = false
				}
				if state == "1" {
					vmixStateMultiple[parameter][input] = true
				}
			}
		}
		fmt.Println(vmixStateSingle)
		fmt.Println(vmixStateMultiple, "\r")
	}
}

// ExecVmixFunc executes a vMix function/action by writing to the API TCP port.  It takes
// a vmixFunc type which includes the action (mandatory), and input and value if appropriate for
// the action.
func ExecVmixFunc(fn *vmixFunc) {
	var message string

	switch fn.action {
	case "Merge":
		message = "FUNCTION Merge Input=" + fn.input
	}

	err := SendMessage(message)
	if err != nil {
		fmt.Println("Unable to send message: ", err)
	}
}

func ProcessMidi() {
	// message is a byte [type button velocity]
	// type 144, velocity 0 is a button up
	// type 144, velocity 127 is a button down
	// type 176 is a control change
	for {
		msg := <-midiMessageChan
		button := int(msg[1])
		var message string

		switch msg[0] {
		case 144:
			if msg[2] == 127 {
				// button pressed
				fmt.Println("Button Down:", msg[1])
				//Check overlayResponses to see if we have a match
				if _, ok := overlayResponses[button]; ok {
					ExecTextOverlay(button)
				}
			}
			if msg[2] == 0 {
				//button released
				fmt.Println("Button Up:", msg[1])
				//Check overlayResponses to see if we have a match. If so remove the overlab
				if _, ok := overlayResponses[button]; ok {
					message = "FUNCTION OverlayInput1Out"
				}
			}

		case 176:
			// Fader moved
			fmt.Println("Control change. Fader:", msg[1], "Value:", msg[2])
		}
		if message != "" {
			_ = SendMessage(message)
		}
	}
}

func listenMidi() {

	rd := reader.New(
		reader.NoLogger(),

		// Fetch every message
		reader.Each(func(pos *reader.Position, msg midi.Message) {
			midiMessageChan <- msg.Raw()
		}),
	)

	err := rd.ListenTo(midiIn)
	if err == nil {
		wg.Wait()
	} else {
		fmt.Println(err)
		wg.Done()
	}
}

// setAPCLED sets the color of APC Mini buttons.  Rectangular buttons
// can be set to green, yellow, or red solid or binking. The round buttons
// can only be on (solid red) or blink (blinking red).  The square button over
// the rightmost fader does not appear to have any LEDs
func setAPCLED(button uint8, color string) {
	values := map[string]uint8{
		"green":       1,
		"greenBlink":  2,
		"red":         3,
		"redBlink":    4,
		"yellow":      5,
		"yellowBlink": 6,
		"on":          1, // for round buttons - they can only be red(on)
		"blink":       2, // or red blinking (blink)
	}
	wr := writer.New(midiOut)
	wr.ConsolidateNotes(false)
	if color == "off" {
		_ = writer.NoteOff(wr, button)
	} else {
		_ = writer.NoteOn(wr, button, values[color])
	}
}

// getMIDIPorts iterates through all midi ports on the driver and identifies the ones
// belonging to an APC Mini.  Returns the input port and output port.
func getMIDIPorts() (midi.In, midi.Out) {
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
				panic("Unable to open MIDI In port")
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
				panic("Unable to open MIDI Out port")
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

func ExecTextOverlay(button int) {
	var message string

	if item, ok := overlayResponses[button]; ok {
		//set the text
		message = "FUNCTION SetText Input=" + item.input + "&SelectedName=" + url.QueryEscape(item.tbName) +
			"&Value=" + url.QueryEscape(item.response)
		_ = SendMessage(message)
		//pause for 100 milliseconds to allow text to update
		d, _ := time.ParseDuration("100ms")
		time.Sleep(d)
		message = "FUNCTION OverlayInput1In Input=" + item.input
		_ = SendMessage(message)
	}
}

func main() {

	wg.Add(2)
	defer vmixClient.conn.Close()
	defer midiIn.Close()
	defer midiOut.Close()
	defer close(vmixMessageChan)
	defer close(midiMessageChan)

	// Processes to listen to the vMix API and act on the messages
	go getMessage()
	go ProcessVmixMessage()

	setAPCLED(7, "green")

	// Listen to the APC Mini and act on button or control changes
	go listenMidi()
	go ProcessMidi()

	wg.Wait()

}
