package main

import (
	"bufio"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	"github.com/davecgh/go-spew/spew"
	"gitlab.com/gomidi/midi"
	"gitlab.com/gomidi/midi/reader"
	"gitlab.com/gomidi/midi/writer"
	"gitlab.com/gomidi/rtmididrv"
	"net"
	"net/url"
	"strconv"
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

type vmixRespConfig struct {
	button   int
	input    int
	tbName   string
	response string
}

type vmixSCConfig struct {
	button          int
	actionsPressed  []string
	actionsReleased []string
}

type vmixPrayerConfig struct {
	button  int
	input   int
	tb1Name string
	text1   string
	tb2Name string
	text2   string
}

type apcLED struct {
	buttons []int
	color   string
	state   string //on or off
}

type vmixActivatorConfig struct {
	trigger   string
	input     int
	onAction  string
	offAction string
}

type vmixFaderConfig struct {
	fader string
	input string
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
var scConfig = make(map[int]*vmixSCConfig)
var respConfig = make(map[int]*vmixRespConfig)
var prayerConfig = make(map[int]*vmixPrayerConfig)
var activatorConfig = make(map[string]*map[int]vmixActivatorConfig)
var faderConfig = make(map[string]*vmixFaderConfig)

func init() {
	//Connect to the vmix API
	err := vmixAPIConnect("192.168.1.173:8099")
	if err != nil {
		fmt.Println("Error connecting to vmix API:")
		panic(err)
	}

	//Get the APC Mini MIDI In and Out ports
	midiIn, midiOut = getMIDIPorts()

	// Send a TALLY command to vMix to get the current setting of active and preview inputs
	// (see ProcessVmixMessage function)
	_ = SendMessage("TALLY")

	// Turn off all LEDs on APC
	setAllLed("off")
}

// readConfig is a blocking function that reads the "responses.xlsx" spreadsheet for configuration
// of shortcuts, overlay text, etc.  This function is intended to run inside a go function.
func readConfig() {
	for {
		wb, err := excelize.OpenFile("responses.xlsx")
		if err != nil {
			fmt.Println("Error opening workbook:", err)
			return
		}

		//Shortcuts
		scRows, _ := wb.GetRows("Shortcuts")
		for idx, row := range scRows {
			if idx != 0 && row != nil {
				btn, _ := strconv.Atoi(row[0])
				cfg := new(vmixSCConfig)
				cfg.button = btn
				cfg.actionsPressed = strings.Split(row[1], "/n")
				if len(row) == 3 {
					cfg.actionsReleased = strings.Split(row[2], ";")
				}
				scConfig[btn] = cfg
			}
		}

		// Responses
		respRows, _ := wb.GetRows("Responses")
		for i, row := range respRows {
			if i != 0 && row != nil {
				btn, _ := strconv.Atoi(row[0])
				input, _ := strconv.Atoi(row[1])
				or := new(vmixRespConfig)
				or.button = btn
				or.input = input
				or.tbName = row[2]
				or.response = row[3]
				respConfig[btn] = or
			}
		}
		// Prayers
		prayerCols, _ := wb.GetCols("Prayers")
		for i, col := range prayerCols {
			if i != 0 && col != nil {
				pr := new(vmixPrayerConfig)
				input, _ := strconv.Atoi(col[1])
				button, _ := strconv.Atoi(col[2])
				pr.input = input
				pr.button = button
				pr.tb1Name = col[3]
				pr.text1 = col[4]
				if len(col) > 5 && col[5] != "----" {
					pr.tb2Name = col[5]
					pr.text2 = col[6]
				}
				prayerConfig[button] = pr
			}
		}

		//Activators
		// map[trigger][input][vmixActivatorConfig]
		activatorCols, _ := wb.GetCols("Activators")

		for i, col := range activatorCols {
			if i > 0 && col != nil {
				var onAction string
				var offAction string
				var trigger string
				var input int
				//col = truncateSlice(col)
				//read the column in chunks of 3 lines, create a vmixActivatorConsole with the info, and
				//add to the inputMap for that trigger
				trigger = col[0]
				inputMap := make(map[int]vmixActivatorConfig)
				for i := 1; col[i] != ""; i = i + 3 {
					input, _ = strconv.Atoi(col[i])
					onAction = col[i+1]
					offAction = col[i+2]
					vmc := new(vmixActivatorConfig)
					vmc.trigger = trigger
					vmc.input = input
					vmc.onAction = onAction
					vmc.offAction = offAction
					inputMap[input] = *vmc
				}
				activatorConfig[trigger] = &inputMap
			}
		}

		// Faders
		faderRows, _ := wb.GetRows("Faders")
		for i := 1; i < len(faderRows); i++ {
			row := faderRows[i]
			fader := row[0]
			input := row[1]
			fc := new(vmixFaderConfig)
			fc.fader = fader
			fc.input = input
			faderConfig[fader] = fc
		}

		duration, _ := time.ParseDuration("5s")
		time.Sleep(duration)
	}
}

// truncateSlice removes empty strings from a slice
func truncateSlice(s []string) []string {
	var r []string
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
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
		} else if strings.Contains(err.Error(), "connection timed out") ||
			strings.Contains(err.Error(), "connection refused") {
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
		vmixMessage := <-vmixMessageChan
		messageSlice := strings.Fields(vmixMessage)
		var input string
		var state string

		// ex:  [ACTS OK InputPlaying 9 1]
		// messageSlice[2] - Action
		// messageSlice[3] - Input
		// messageSlice[4] - Value (usually 0 for off, 1 for on)

		if messageSlice[0] == "ACTS" && messageSlice[1] == "OK" {
			processActivator(vmixMessage)
			parameter := messageSlice[2]

			if len(messageSlice) == 4 {
				state = messageSlice[3]
			}

			if len(messageSlice) == 5 {
				input = messageSlice[3]
				state = messageSlice[4]
			}

			switch parameter {
			case "Streaming", "Recording":
				vmixStateSingle[parameter] = state

			case "Input", "InputPreview", "Overlay1", "Overlay2", "Overlay3", "Overlay4", "":

				if state == "1" {
					// update vMix State Map
					vmixStateSingle[parameter] = input
				}
				if state == "0" {
					// update vMix State Map
					if vmixStateSingle[parameter] == input {
						vmixStateSingle[parameter] = ""
					}
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

		if messageSlice[0] == "TALLY" && messageSlice[1] == "OK" {
			//Tally message received.  This tells us the current state of Active and Preview.  One is
			//sent during initialization.  Use this to update the vMix state maps.  The tally string is a
			//string of numbers.  The position in the string corresponds to the input number.  1 indicates that
			//input is the active input, 2 indicates it is in Preview. Example:
			//  TALLY OK 0000000000000000200000000001000000000
			tally := messageSlice[2]
			var i int

			activeIdx := strings.Index(tally, "1")
			previewIdx := strings.Index(tally, "2")

			if activeIdx > -1 {
				i = activeIdx + 1
				vmixStateSingle["Input"] = strconv.Itoa(i)
			}

			if previewIdx > -1 {
				i = previewIdx + 1
				vmixStateSingle["InputPreview"] = strconv.Itoa(i)
			}
		}
	}
}

func processActivator(vmixMessage string) {
	messageSlice := strings.Fields(vmixMessage)
	trigger := messageSlice[2]
	var state string
	var input int
	var actions string

	if len(messageSlice) == 5 {
		state = messageSlice[4]
		input, _ = strconv.Atoi(messageSlice[3])
	}

	if len(messageSlice) == 4 {
		state = messageSlice[3]
		input = 0
	}

	if _, ok := activatorConfig[trigger]; ok { //do we have an activator config for this trigger?
		v := *activatorConfig[trigger]
		if _, ok := v[input]; ok { //do we have an activator config for this trigger and input?
			if state == "0" {
				actions = v[input].offAction
				actSlice := strings.Split(actions, ";")
				if len(actSlice) > 0 {
					for _, action := range actSlice {
						act := strings.Split(action, ": ")
						color := act[0]
						buttons := strings.Split(act[1], ",")
						iButtons := make([]int, len(buttons))
						for i, s := range buttons {
							iButtons[i], _ = strconv.Atoi(s)
						}
						apcLED := new(apcLED)
						apcLED.buttons = iButtons
						apcLED.color = color
						setAPCLED(apcLED)
					}
				}
			}

			if state == "1" {
				actions = v[input].onAction
				actSlice := strings.Split(actions, ";")
				if _, ok := v[input]; ok { //do we have an activator config for this trigger and input?
					for _, action := range actSlice {
						act := strings.Split(action, ": ")
						color := act[0]
						buttons := strings.Split(act[1], ",")
						iButtons := make([]int, len(buttons))
						for i, s := range buttons {
							iButtons[i], _ = strconv.Atoi(s)
						}
						apcLED := new(apcLED)
						apcLED.buttons = iButtons
						apcLED.color = color
						setAPCLED(apcLED)
					}
				}
			}
		}

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
		var message []string

		switch msg[0] {
		case 144:
			if msg[2] == 127 {
				// button pressed
				fmt.Println("Button Down:", msg[1])
				//Check overlayResponses to see if we have a match
				if _, ok := respConfig[button]; ok {
					ExecTextOverlay(button)
				}

				if _, ok := prayerConfig[button]; ok {
					ExecPrayerOverlay(button)
				}

				if _, ok := scConfig[button]; ok {
					for _, action := range scConfig[button].actionsPressed {
						m := "FUNCTION " + action + "\r\n"
						message = append(message, m)
					}
				}
			}
			if msg[2] == 0 {
				//button released
				fmt.Println("Button Up:", msg[1])
				//Check respConfig to see if we have a match. If so remove the overlay
				if _, ok := respConfig[button]; ok {
					message = append(message, "FUNCTION OverlayInput1Out")
				}

				if _, ok := prayerConfig[button]; ok {
					message = append(message, "FUNCTION OverlayInput1Out")
				}
			}

		case 176:
			// Fader moved
			fader := strconv.Itoa(int(msg[1]))

			if _, ok := faderConfig[fader]; ok {
				input := faderConfig[fader].input
				value := int(msg[2])
				volume := (value * 100) / 127 // SetVolume expects a value 0-100. APC gives 0-127
				volumeS := strconv.Itoa(volume)
				var m string

				_, err := strconv.Atoi(input)
				if err == nil {
					//input is numeric. Set the volume on the appropriate input number
					m = "FUNCTION SetVolume Input=" + input + "&Value=" + volumeS
				} else {
					//input is textual. Need to set a bus or master
					if input == "Master" {
						m = "FUNCTION SetMasterVolume Value=" + volumeS
					}

					if strings.Contains(input, "Bus") {
						m = "FUNCTION Set" + input + "Volume Value=" + volumeS
					}
				}
				if m != "" {
					message = append(message, m)
				}

			}
		}

		if message != nil {
			for _, mess := range message {
				_ = SendMessage(mess)
			}
		}

		if button == 98 {
			spew.Dump(vmixStateSingle)
			spew.Dump(vmixStateMultiple)
		}
	}
}

func ExecTextOverlay(button int) {
	var message string

	if item, ok := respConfig[button]; ok {
		input := strconv.Itoa(item.input)
		//set the text
		message = "FUNCTION SetText Input=" + input + "&SelectedName=" + url.QueryEscape(item.tbName) +
			"&Value=" + url.QueryEscape(item.response)
		_ = SendMessage(message)
		fmt.Println(message)
		//pause for 100 milliseconds to allow text to update in the title
		d, _ := time.ParseDuration("100ms")
		time.Sleep(d)
		message = "FUNCTION OverlayInput1In Input=" + input
		_ = SendMessage(message)
	}
}

func ExecPrayerOverlay(button int) {
	fmt.Println("Starting ExecPrayerOverlay")
	var message string

	if item, ok := prayerConfig[button]; ok {
		input := strconv.Itoa(item.input)
		message = "FUNCTION SetText Input=" + input + "&SelectedName=" + url.QueryEscape(item.tb1Name) +
			"&Value=" + url.QueryEscape(item.text1)
		_ = SendMessage(message)
		if item.tb2Name != "" {
			message = "FUNCTION SetText Input=" + input + "&SelectedName=" + url.QueryEscape(item.tb2Name) +
				"&Value=" + url.QueryEscape(item.text2)
			_ = SendMessage(message)
		}
		d, _ := time.ParseDuration("100ms")
		time.Sleep(d)
		message = "FUNCTION OverlayInput1In Input=" + input
		_ = SendMessage(message)
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
func setAPCLED(led *apcLED) {
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

	for _, button := range led.buttons {
		b := uint8(button)
		if led.color == "off" {
			_ = writer.NoteOff(wr, b)
		} else {
			_ = writer.NoteOn(wr, b, values[led.color])
		}
	}

}

func setAllLed(color string) {
	led := new(apcLED)
	min := 0
	max := 71
	a := make([]int, max-min+1)
	for i := range a {
		a[i] = min + i
	}
	led.color = color
	led.buttons = a
	setAPCLED(led)

	min = 82
	max = 89
	a = make([]int, max-min+1)
	for i := range a {
		a[i] = min + i
	}
	led.buttons = a
	setAPCLED(led)
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

func main() {

	wg.Add(2)
	defer vmixClient.conn.Close()
	defer midiIn.Close()
	defer midiOut.Close()
	defer setAllLed("off")
	defer close(vmixMessageChan)
	defer close(midiMessageChan)

	go readConfig()

	// Processes to listen to the vMix API and act on the messages
	go getMessage()
	go ProcessVmixMessage()

	// Listen to the APC Mini and act on button or control changes
	go listenMidi()
	go ProcessMidi()

	wg.Wait()

}
