package main

import (
	"bufio"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	"github.com/beevik/etree"
	"github.com/davecgh/go-spew/spew"
	"github.com/radovskyb/watcher"
	"gitlab.com/gomidi/midi"
	"gitlab.com/gomidi/midi/reader"
	"gitlab.com/gomidi/midi/writer"
	"gitlab.com/gomidi/rtmididrv"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DEBUG = true

type vmixClient struct {
	conn        net.Conn
	w           *bufio.Writer
	r           *bufio.Reader
	connected   bool
	apiAddress  string
	messageChan chan string
	wg          *sync.WaitGroup
	lock        sync.RWMutex
}

type vcConfig struct {
	apiAddress  string
	messageChan chan string
	wg          *sync.WaitGroup
}

type response struct {
	button   int
	input    int
	tbName   string
	response string
}

type shortcut struct {
	button          int
	actionsPressed  []string
	actionsReleased []string
}

type prayer struct {
	button  int
	input   int
	tb1Name string
	text1   string
	tb2Name string
	text2   string
}

type activator struct {
	trigger   string
	input     int
	onAction  []string
	offAction []string
}

type fader struct {
	fader int
	input string
}

type config struct {
	fader     map[int]*fader
	activator map[string]*map[int]activator
	prayer    map[int]*prayer
	shortcut  map[int]*shortcut
	response  map[int]*response
}

type state struct {
	Input            int
	InputPreview     int
	Overlay1         int
	Overlay2         int
	Overlay3         int
	Overlay4         int
	Overlay5         int
	Overlay6         int
	Streaming        int
	Recording        int
	InputPlaying     map[int]bool
	InputMasterAudio map[int]bool
	InputBusAAudio   map[int]bool
	InputBusBAudio   map[int]bool
}

type midiPorts struct {
	in  *midi.In
	out *midi.Out
}

type apcLEDS struct {
	buttons []int
	color   string
}

// Translate from the APC midi mapping (0 is left button on last row
// to more logical numbering where 1 is the top-left button
var hButton = []int{
	57, 58, 59, 60, 61, 62, 63, 64, //Rectangular buttons
	49, 50, 51, 52, 53, 54, 55, 56,
	41, 42, 43, 44, 45, 46, 47, 48,
	33, 34, 35, 36, 37, 38, 39, 40,
	24, 26, 27, 28, 29, 30, 31, 32,
	17, 18, 19, 20, 21, 22, 23, 24,
	9, 10, 11, 12, 13, 14, 15, 16,
	1, 2, 3, 4, 5, 6, 7, 8,
	65, 66, 67, 68, 69, 70, 71, 72, //Horizontal round buttons
	99, 99, 99, 99, 99, 99, 99, 99,
	99, 99,
	73, 74, 75, 76, 77, 78, 79, 80, //Vertical round buttons
	99, 99, 99, 99, 99, 99, 99, 99,
	81, // Square button
}

// Translate from more logical numbering to
// APC midi mapping.
var oButton = []int{
	99, 56, 57, 58, 59, 60, 61, 62, 63,
	48, 49, 50, 51, 52, 53, 54, 55,
	40, 41, 42, 43, 44, 45, 46, 47,
	32, 33, 34, 35, 36, 37, 38, 39,
	24, 25, 26, 27, 28, 29, 30, 31,
	16, 17, 18, 19, 20, 21, 22, 23,
	8, 9, 10, 11, 12, 13, 14, 15,
	0, 1, 2, 3, 4, 5, 6, 7,
	65, 66, 67, 68, 69, 70, 71, 72,
	73, 74, 75, 76, 77, 78, 79, 80,
	99, 99, 99, 99, 99, 99, 99, 99,
	81,
}

var oFader = map[int]int{
	48: 1,
	49: 2,
	50: 3,
	51: 4,
	52: 5,
	53: 6,
	54: 7,
	55: 8,
	56: 9,
}

func debug(msg ...interface{}) {
	if DEBUG {
		fmt.Println(msg)
	}
}

func newState() state {
	var vmixState = new(state)
	vmixState.InputBusAAudio = make(map[int]bool)
	vmixState.InputBusBAudio = make(map[int]bool)
	vmixState.InputMasterAudio = make(map[int]bool)
	vmixState.InputPlaying = make(map[int]bool)
	return *vmixState
}

// updateVmixState will create a connection to the vMix API and query it to update the
// vMix state variables with the current configuration
func updateVmixState(vc vcConfig) state {
	client, _ := vmixAPIConnect(vc)
	vmixState := newState()
	_, err := client.w.WriteString("XML\r\n")
	if err == nil {
		err = client.w.Flush()
	}
	var xml string
	var cont bool
	for cont = true; cont; {
		line, _ := client.r.ReadString('\r')
		if strings.Contains(line, "<vmix>") {
			xml = xml + line
		}
		if strings.Contains(line, "</vmix>") {
			xml = xml + line
			cont = false
		}
	}

	doc := etree.NewDocument()
	_ = doc.ReadFromString(xml)

	for _, overlays := range doc.FindElements("./vmix/overlays/*") {
		number := overlays.SelectAttrValue("number", "")
		input, _ := strconv.Atoi(overlays.Text())
		if input > 0 {

			switch number {
			case "1":
				vmixState.Overlay1 = input
			case "2":
				vmixState.Overlay1 = input
			case "3":
				vmixState.Overlay1 = input
			case "4":
				vmixState.Overlay1 = input
			case "5":
				vmixState.Overlay1 = input
			case "6":
				vmixState.Overlay1 = input
			}
		}
	}

	for _, inputs := range doc.FindElements("./vmix/inputs/*") {
		busses := inputs.SelectAttrValue("audiobusses", "")
		number, _ := strconv.Atoi(inputs.SelectAttrValue("number", ""))
		inputType := inputs.SelectAttrValue("type", "")
		state := inputs.SelectAttrValue("state", "")

		if busses != "" {
			if strings.Contains(busses, "A") {
				//vmixStateMultiple["InputBusAAudio"] = map[string]bool{number: true}
				vmixState.InputBusAAudio[number] = true
			}
			if strings.Contains(busses, "B") {
				//vmixStateMultiple["InputBusBAudio"] = map[string]bool{number: true}
				vmixState.InputBusBAudio[number] = true
			}
			if strings.Contains(busses, "M") {
				//vmixStateMultiple["InputMasterAudio"] = map[string]bool{number: true}
				vmixState.InputMasterAudio[number] = true
			}
		}

		if inputType == "Video" && state == "Running" {
			//vmixStateMultiple["InputPlaying"] = map[string]bool{number: true}
			vmixState.InputPlaying[number] = true
		}
	}

	streaming := doc.FindElement("/vmix/streaming").Text()
	if streaming == "True" {
		vmixState.Streaming = 1
	} else {
		vmixState.Streaming = 0
	}
	recording := doc.FindElement("/vmix/recording").Text()
	if recording == "True" {
		vmixState.Recording = 1
	} else {
		vmixState.Recording = 0
	}

	active := doc.FindElement("/vmix/active").Text()
	vmixState.Input, _ = strconv.Atoi(active)

	preview := doc.FindElement("/vmix/preview").Text()
	vmixState.InputPreview, _ = strconv.Atoi(preview)

	return vmixState
}

// newConfig initializes the configuration variable and loads it with the content of the configuration
// spreadsheet.  It returns the new configuration variable
func newConfig() config {

	var scConfig = make(map[int]*shortcut)
	var respConfig = make(map[int]*response)
	var prayerConfig = make(map[int]*prayer)
	var activatorConfig = make(map[string]*map[int]activator)
	var faderConfig = make(map[int]*fader)
	conf := config{
		fader:     faderConfig,
		activator: activatorConfig,
		prayer:    prayerConfig,
		shortcut:  scConfig,
		response:  respConfig,
	}

	wb, err := excelize.OpenFile("responses.xlsx")
	if err != nil {
		fmt.Println("Error opening workbook:", err)
		return conf
	}

	//Shortcuts
	scRows, _ := wb.GetRows("Shortcuts")
	for idx, row := range scRows {
		if idx != 0 && len(row) > 1 {
			btn, _ := strconv.Atoi(row[0])
			cfg := new(shortcut)
			cfg.button = btn
			cfg.actionsPressed = strings.Split(row[1], "\n")
			if len(row) > 2 {
				cfg.actionsReleased = strings.Split(row[2], "\n")
			}
			conf.shortcut[btn] = cfg
		}
	}

	// Responses
	respRows, _ := wb.GetRows("Responses")
	for i, row := range respRows {
		if i != 0 && len(row) > 1 {
			btn, _ := strconv.Atoi(row[0])
			input, _ := strconv.Atoi(row[1])
			or := new(response)
			or.button = btn
			or.input = input
			or.tbName = row[2]
			or.response = row[3]
			conf.response[btn] = or
		}
	}
	// Prayers
	prayerCols, _ := wb.GetCols("Prayers")
	for i, col := range prayerCols {
		if i != 0 && col != nil {
			pr := new(prayer)
			input, _ := strconv.Atoi(col[1])
			btn, _ := strconv.Atoi(col[2])
			pr.input = input
			pr.button = btn
			pr.tb1Name = col[3]
			pr.text1 = col[4]
			if len(col) > 5 && col[5] != "----" {
				pr.tb2Name = col[5]
				pr.text2 = col[6]
			}
			conf.prayer[btn] = pr
		}
	}

	//Activators
	// map[trigger][input][vmixActivatorConfig]
	activatorCols, _ := wb.GetCols("Activators")

	for i, col := range activatorCols {
		if i > 0 && col != nil {
			var onActions []string
			var offActions []string
			var trigger string
			var input int

			//read the column in chunks of 3 lines, create a vmixActivatorConsole with the info, and
			//add to the inputMap for that trigger
			trigger = col[0]
			inputs := make(map[int]activator)
			for i := 1; col[i] != ""; i = i + 3 {
				input, _ = strconv.Atoi(col[i])
				onActions = strings.Split(col[i+1], "\n")
				offActions = strings.Split(col[i+2], "\n")
				vmc := new(activator)
				vmc.trigger = trigger
				vmc.input = input
				vmc.onAction = onActions
				vmc.offAction = offActions
				inputs[input] = *vmc
			}
			conf.activator[trigger] = &inputs
		}
	}

	// Faders
	faderRows, _ := wb.GetRows("Faders")
	for i := 1; i < len(faderRows); i++ {
		row := faderRows[i]
		faderNum, _ := strconv.Atoi(row[0])
		input := row[1]
		fc := new(fader)
		fc.fader = faderNum
		fc.input = input
		conf.fader[faderNum] = fc
	}
	return conf
}

// watchConfigFile watches the configuration spreadsheet (responses.xlsx) for any changes (write).
// If any changes are detected it will reload the conf variable with the new data.
func watchConfigFile(conf *config, fileName string) {
	w := watcher.New()

	go func() {
		for {
			select {
			case event := <-w.Event:
				if event.Op.String() == "WRITE" {
					updateConfig(conf, fileName)
				}
			case err := <-w.Error:
				log.Fatalln(err)
			case <-w.Closed:
				return
			}
		}
	}()

	if err := w.Add(fileName); err != nil {
		log.Fatalln(err)
	}
	if err := w.Start(time.Millisecond * 100); err != nil {
		log.Fatalln(err)
	}

}

// updateConfig will update the current config variable by re-reading the configuration spreadsheet.  It is
// intended to be called by a file watcher whenever the configuration sheet is changed.
func updateConfig(conf *config, fileName string) {
	wb, err := excelize.OpenFile(fileName)
	if err != nil {
		fmt.Println("Error opening workbook:", err)
	}

	//Shortcuts
	scRows, _ := wb.GetRows("Shortcuts")
	for idx, row := range scRows {
		if idx != 0 && row != nil && len(row) > 1 {
			btn, _ := strconv.Atoi(row[0])
			cfg := new(shortcut)
			cfg.button = btn
			cfg.actionsPressed = strings.Split(row[1], "\n")
			if len(row) > 2 {
				cfg.actionsReleased = strings.Split(row[2], "\n")
			}
			conf.shortcut[btn] = cfg
		}
	}

	// Responses
	respRows, _ := wb.GetRows("Responses")
	for i, row := range respRows {
		if i != 0 && row != nil {
			btn, _ := strconv.Atoi(row[0])
			input, _ := strconv.Atoi(row[1])
			or := new(response)
			or.button = hButton[btn]
			or.input = input
			or.tbName = row[2]
			or.response = row[3]
			conf.response[hButton[btn]] = or
		}
	}
	// Prayers
	prayerCols, _ := wb.GetCols("Prayers")
	for i, col := range prayerCols {
		if i != 0 && col != nil {
			pr := new(prayer)
			input, _ := strconv.Atoi(col[1])
			btn, _ := strconv.Atoi(col[2])
			pr.input = input
			pr.button = btn
			pr.tb1Name = col[3]
			pr.text1 = col[4]
			if len(col) > 5 && col[5] != "----" {
				pr.tb2Name = col[5]
				pr.text2 = col[6]
			}
			conf.prayer[btn] = pr
		}
	}

	//Activators
	activatorCols, _ := wb.GetCols("Activators")

	for i, col := range activatorCols {
		if i > 0 && col != nil {
			var onActions []string
			var offActions []string
			var trigger string
			var input int

			trigger = col[0]
			inputs := make(map[int]activator)
			for i := 1; col[i] != ""; i = i + 3 {
				input, _ = strconv.Atoi(col[i])
				onActions = strings.Split(col[i+1], "\n")
				offActions = strings.Split(col[i+2], "\n")
				vmc := new(activator)
				vmc.trigger = trigger
				vmc.input = input
				vmc.onAction = onActions
				vmc.offAction = offActions
				inputs[input] = *vmc
			}
			conf.activator[trigger] = &inputs
		}
	}

	// Faders
	faderRows, _ := wb.GetRows("Faders")
	for i := 1; i < len(faderRows); i++ {
		row := faderRows[i]
		faderNum, _ := strconv.Atoi(row[0])
		input := row[1]
		fc := new(fader)
		fc.fader = faderNum
		fc.input = input
		conf.fader[faderNum] = fc
	}
}

// vmixAPIConnect connects to the vMix API. apiAddress is a string
// of the format ipaddress:port.  By default, the vMix API is on port 8099.
// If vMix is not up, this function will continue trying to connect, and will
// block until a connection is achieved.
func vmixAPIConnect(vc vcConfig) (*vmixClient, error) {
	client := new(vmixClient)
	client.connected = false
	client.apiAddress = vc.apiAddress
	client.wg = vc.wg
	client.messageChan = vc.messageChan

	for client.connected == false {
		timeout, _ := time.ParseDuration("20s")
		conn, err := net.DialTimeout("tcp", client.apiAddress, timeout)

		if err == nil {
			client.conn = conn
			client.w = bufio.NewWriter(conn)
			client.r = bufio.NewReader(conn)
			client.connected = true
		} else if strings.Contains(err.Error(), "connection timed out") ||
			strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "timeout") {
			fmt.Println("vmix api is inaccessible.  Probably because vMix is not running. Error received is:", err)
			fmt.Println("Waiting 5 seconds and trying again")
			client.connected = false
			time.Sleep(5)
		} else {
			fmt.Println("Unable to connect. Error was: ", err)
			return client, err
		}
	}
	return client, nil
}

// SendMessage sends a message to the vMix API. It adds the
// /r/n terminator the API expects
func SendMessage(client *vmixClient, message string) error {

	pub := fmt.Sprintf("%v\r\n", message)
	_, err := client.w.WriteString(pub)
	if err == nil {
		err = client.w.Flush()
	}
	debug("Sent message to API:", message)
	return err
}

// getMessage connects to the vMix API and issues a subscription to activators.
// It then remains listening for any messages from the API server.  Any messages
// received are sent to the vmixMessageChan channel for consumption.  This is a blocking
// function.  The vmixClient must already be connected to the API and available as a global object.
func getMessage(client *vmixClient) {

	// Subscribe to the activator feed in the vMix API

	err := SendMessage(client, "SUBSCRIBE ACTS")
	if err != nil {
		fmt.Println("Error in GetMessage.SendMessage: ", err)
		client.wg.Done()
	}

	//Capture all responses from the vMix API
	for {
		line, err := client.r.ReadString('\n')

		if err == nil {
			client.messageChan <- line
			debug("Received from API:", line)
		} else {
			client.wg.Done()
			fmt.Println("Error in GetMessage.ReadString: ", err)
		}
	}
}

// processVmixMessage listens to the vMix API channel for any messages from the API.
// It uses these messages to update the vMix State maps which are used for the
// conditional actions. This is a blocking function.
func processVmixMessage(client *vmixClient, midiOutChan chan apcLEDS, vmixState state, conf config) {
	for {
		vmixMessage := <-client.messageChan
		messageSlice := strings.Fields(vmixMessage)
		var input int
		var state int

		if messageSlice[0] == "ACTS" && messageSlice[1] == "OK" {
			debug("Processing message:", vmixMessage)
			processActivator(vmixMessage, midiOutChan, conf)
			parameter := messageSlice[2]

			if len(messageSlice) == 4 {
				state, _ = strconv.Atoi(messageSlice[3])
			}

			if len(messageSlice) == 5 {
				input, _ = strconv.Atoi(messageSlice[3])
				state, _ = strconv.Atoi(messageSlice[4])
			}

			switch parameter {
			case "Input":
				vmixState.Input = input
			case "InputPreview":
				vmixState.InputPreview = input
			case "Overlay1":
				if state == 1 {
					vmixState.Overlay1 = input
				} else {
					vmixState.Overlay1 = 0
				}
			case "Overlay2":
				if state == 1 {
					vmixState.Overlay2 = input
				} else {
					vmixState.Overlay2 = 0
				}
			case "Overlay3":
				if state == 1 {
					vmixState.Overlay3 = input
				} else {
					vmixState.Overlay3 = 0
				}
			case "Overlay4":
				if state == 1 {
					vmixState.Overlay4 = input
				} else {
					vmixState.Overlay4 = 0
				}
			case "Overlay5":
				if state == 1 {
					vmixState.Overlay5 = input
				} else {
					vmixState.Overlay5 = 0
				}
			case "Overlay6":
				if state == 1 {
					vmixState.Overlay6 = input
				} else {
					vmixState.Overlay6 = 0
				}
			case "Streaming":
				vmixState.Streaming = state
			case "Recording":
				vmixState.Recording = state
			case "InputPlaying":
				switch state {
				case 0:
					vmixState.InputPlaying = map[int]bool{input: false}
				case 1:
					vmixState.InputPlaying = map[int]bool{input: true}
				}
			case "InputBusAAudio":
				switch state {
				case 0:
					vmixState.InputBusAAudio = map[int]bool{input: false}
				case 1:
					vmixState.InputBusAAudio = map[int]bool{input: true}
				}
			case "InputBusBAudio":
				switch state {
				case 0:
					vmixState.InputBusBAudio = map[int]bool{input: false}
				case 1:
					vmixState.InputBusBAudio = map[int]bool{input: true}
				}
			}
		}

	}
}

func processActivator(vmixMessage string, midiOutChan chan apcLEDS, conf config) {

	messageSlice := strings.Fields(vmixMessage)
	trigger := messageSlice[2]
	var state string
	var input int
	var actions []string

	if len(messageSlice) == 5 {
		state = messageSlice[4]
		input, _ = strconv.Atoi(messageSlice[3])
	}

	if len(messageSlice) == 4 {
		state = messageSlice[3]
		input = 0
	}

	if _, ok := conf.activator[trigger]; ok { //do we have an activator config for this trigger?
		v := *conf.activator[trigger]
		if _, ok := v[input]; ok { //do we have an activator config for this trigger and input?
			if state == "0" {
				actions = v[input].offAction
				for _, action := range actions {
					debug(action)
					act := strings.Split(action, ": ")
					color := act[0]
					buttons := strings.Split(act[1], ",")
					iButtons := make([]int, len(buttons))
					for i, s := range buttons {
						iButtons[i], _ = strconv.Atoi(s)

					}

					leds := apcLEDS{
						buttons: iButtons,
						color:   color,
					}
					midiOutChan <- leds
				}
			}
		}

		if state == "1" {
			actions = v[input].onAction
			for _, action := range actions {
				act := strings.Split(action, ": ")
				color := act[0]
				buttons := strings.Split(act[1], ",")
				iButtons := make([]int, len(buttons))
				for i, s := range buttons {
					iButtons[i], _ = strconv.Atoi(s)

				}

				leds := apcLEDS{
					buttons: iButtons,
					color:   color,
				}
				midiOutChan <- leds
			}
		}
	}
}

func processMidi(midiInChan chan []byte, midiOutChan chan apcLEDS, client *vmixClient, vmixState state, conf config) {
	// message is a byte [type button velocity]
	// type 144, velocity 0 is a button up
	// type 144, velocity 127 is a button down
	// type 176 is a control change
	for {
		msg := <-midiInChan
		button := hButton[int(msg[1])]
		var message []string

		switch msg[0] {
		case 144:
			if msg[2] == 127 {
				// button pressed
				debug("Button Down:", msg[1], button)
				//Check overlayResponses to see if we have a match
				if _, ok := conf.response[button]; ok {
					execTextOverlay(client, button, conf)
					midiOutChan <- apcLEDS{
						buttons: []int{button},
						color:   "red",
					}

				}

				if _, ok := conf.prayer[button]; ok {
					execPrayerOverlay(client, button, vmixState, conf)
				}

				if _, ok := conf.shortcut[button]; ok {
					for _, action := range conf.shortcut[button].actionsPressed {
						debug("Performing action:", action)
						if action == "dumpVars" {
							spew.Dump("vmixState", vmixState)
							spew.Dump("Config", conf)
						} else if strings.HasPrefix(action, "leds") {
							// ex: leds green 1,2,3
							parts := strings.Split(action, " ")
							color := parts[1]
							leds := strings.Split(parts[2], ",")
							iLeds := make([]int, len(leds))
							for i, s := range leds {
								iLeds[i], _ = strconv.Atoi(s)
							}
							midiOutChan <- apcLEDS{
								buttons: iLeds,
								color:   color,
							}

						} else {
							m := "FUNCTION " + action
							message = append(message, m)
						}
					}
				}
			}
			if msg[2] == 0 {
				//button released
				debug("Button Up:", msg[1], button)
				//Check respConfig to see if we have a match. If so remove the overlay
				if _, ok := conf.response[button]; ok {
					message = append(message, "FUNCTION OverlayInput1Out")
					midiOutChan <- apcLEDS{
						buttons: []int{button},
						color:   "off",
					}

				}

				if _, ok := conf.shortcut[button]; ok {
					for _, action := range conf.shortcut[button].actionsReleased {
						debug("Shortcut action to be taken:", action)
						if action != "" {
							if strings.HasPrefix(action, "leds") {
								// ex: leds green 1,2,3
								parts := strings.Split(action, " ")
								color := parts[1]
								leds := strings.Split(parts[2], ",")
								iLeds := make([]int, len(leds))
								for i, s := range leds {
									iLeds[i], _ = strconv.Atoi(s)
								}
								midiOutChan <- apcLEDS{
									buttons: iLeds,
									color:   color,
								}

							} else {
								m := "FUNCTION " + action + "\r\n"
								message = append(message, m)
							}
						}
					}
				}
			}

		case 176:
			// Fader moved
			fader := oFader[int(msg[1])]

			if _, ok := conf.fader[fader]; ok {
				input := conf.fader[fader].input
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
				debug("Sending message:", mess)
				_ = SendMessage(client, mess)
			}
		}
	}
}

func execTextOverlay(client *vmixClient, button int, conf config) {
	var message string

	if item, ok := conf.response[button]; ok {
		input := strconv.Itoa(item.input)
		//set the text
		message = "FUNCTION SetText Input=" + input + "&SelectedName=" + url.QueryEscape(item.tbName) +
			"&Value=" + url.QueryEscape(item.response)
		_ = SendMessage(client, message)
		//pause for 100 milliseconds to allow text to update in the title
		d, _ := time.ParseDuration("100ms")
		time.Sleep(d)
		message = "FUNCTION OverlayInput1In Input=" + input
		_ = SendMessage(client, message)
	}
}

func execPrayerOverlay(client *vmixClient, button int, vmixState state, conf config) {
	var message string

	if item, ok := conf.prayer[button]; ok {
		input := strconv.Itoa(item.input)

		if vmixState.Overlay1 == item.input {
			//Overlay is already displayed, remove it
			message = "FUNCTION OverlayInput1Out"
		} else {
			// Display the overlay
			message = "FUNCTION SetText Input=" + input + "&SelectedName=" + url.QueryEscape(item.tb1Name) +
				"&Value=" + url.QueryEscape(item.text1)
			_ = SendMessage(client, message)
			if item.tb2Name != "" {
				message = "FUNCTION SetText Input=" + input + "&SelectedName=" + url.QueryEscape(item.tb2Name) +
					"&Value=" + url.QueryEscape(item.text2)
				_ = SendMessage(client, message)
			}
			d, _ := time.ParseDuration("100ms")
			time.Sleep(d)
			message = "FUNCTION OverlayInput1In Input=" + input
			_ = SendMessage(client, message)
		}
	}
}

func setAllLed(color string, midiOutChan chan apcLEDS) {

	min := 1
	max := 63
	a := make([]int, max-min+1)
	for i := range a {
		a[i] = min + i
	}

	leds := apcLEDS{
		buttons: a,
		color:   color,
	}
	midiOutChan <- leds

	/*min = 82
	max = 89
	a = make([]int, max-min+1)
	for i := range a {
		a[i] = min + i
	}

	leds = apcLEDS{
		buttons: a,
		color:   color,
	}
	midiOutChan <- leds*/
}

func getMIDIPorts() (midiPort midiPorts) {
	var inPort midi.In
	var outPort midi.Out
	foundAPCIn := false
	foundAPCOut := false

	drv, err := rtmididrv.New()
	if err != nil {
		fmt.Printf("Unable to open MIDI Driver")
		return
	}

	inPorts, _ := drv.Ins()
	outPorts, _ := drv.Outs()

	if len(inPorts) == 0 || len(outPorts) == 0 {
		fmt.Println("No MIDI ports found. Aborting")
		return
	}

	for _, port := range inPorts {

		if strings.Contains(port.String(), "APC MINI") {
			inPort, err = midi.OpenIn(drv, port.Number(), "")
			if err != nil {
				fmt.Printf("Unable to open APC MIDI In port")
				return
			} else {
				foundAPCIn = true
			}
		}
	}

	for _, port := range outPorts {

		if strings.Contains(port.String(), "APC MINI") {
			outPort, err = midi.OpenOut(drv, port.Number(), "")
			if err != nil {
				fmt.Println("Unable to open APC MIDI Out port")
				return
			} else {
				foundAPCOut = true
			}
		}
	}

	if foundAPCIn && foundAPCOut {
		midiPort.in = &inPort
		midiPort.out = &outPort
		return midiPort
	} else {
		panic("No APC Mini found. Aborting")
		return
	}
}

func initMidi(midiInChan chan []byte, midiOutChan chan apcLEDS) {
	var midiPort = new(midiPorts)

	*midiPort = getMIDIPorts()

	rd := reader.New(
		reader.NoLogger(),

		// Fetch every message
		reader.Each(func(pos *reader.Position, msg midi.Message) {
			midiInChan <- msg.Raw()
		}),
	)

	err := rd.ListenTo(*midiPort.in)
	if err != nil {
		fmt.Println(err)
		//client.wg.Done()
		return
	}

	for {
		apcLED := <-midiOutChan
		setAPCLED(apcLED, midiPort.out)
	}
}

func setAPCLED(led apcLEDS, outPort *midi.Out) {
	values := map[string]uint8{
		"green":       1,
		"greenBlink":  2,
		"red":         3,
		"redBlink":    4,
		"yellow":      5,
		"yellowBlink": 6,
		"on":          1, // for round buttons - they can only be red/green (on)
		"blink":       2, // or red/green blinking (blink)
	}
	wr := writer.New(*outPort)
	wr.ConsolidateNotes(false)

	for _, button := range led.buttons {
		b := oButton[button]
		debug("LED request.  orig button:", button, "trans button:", b, " Color:", led.color)
		if led.color == "off" {
			_ = writer.NoteOff(wr, uint8(b))
		} else {
			_ = writer.NoteOn(wr, uint8(b), values[led.color])
		}
	}
}

func main() {
	const (
		apiAddress = "127.0.0.1:8099"   // address and port for the vMix TCP API
		fileName   = "./responses.xlsx" // path and filename to the configuration spreadsheet
	)

	var midiInChan = make(chan []byte, 10)
	var midiOutChan = make(chan apcLEDS, 10)
	var messageChan = make(chan string)
	var wg sync.WaitGroup

	vcConf := vcConfig{
		apiAddress:  apiAddress,
		messageChan: messageChan,
		wg:          &wg,
	}

	vmClient, _ := vmixAPIConnect(vcConf)

	vmixState := updateVmixState(vcConf)
	config := newConfig()
	go watchConfigFile(&config, fileName)

	setAllLed("off", midiOutChan)

	go getMessage(vmClient)
	go processVmixMessage(vmClient, midiOutChan, vmixState, config)

	go initMidi(midiInChan, midiOutChan)
	go processMidi(midiInChan, midiOutChan, vmClient, vmixState, config)

	defer vmClient.conn.Close()
	defer close(vmClient.messageChan)
	defer close(midiInChan)

	wg.Add(2)
	wg.Wait()
}
