package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	"github.com/beevik/etree"
	"github.com/mitchellh/go-ps"
	"github.com/use-go/onvif"
	"github.com/use-go/onvif/ptz"
	onvif2 "github.com/use-go/onvif/xsd/onvif"
	"gitlab.com/gomidi/midi"
	"gitlab.com/gomidi/midi/reader"
	"gitlab.com/gomidi/midi/writer"
	"gitlab.com/gomidi/rtmididrv"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
	input    string
	tbName   string
	response string
}

type shortcut struct {
	button          int
	actionsPressed  []string
	actionsReleased []string
}

type prayer struct {
	button int
	input  string
	tbName string
	verses []string
}

type pop struct {
	button      int
	input       string
	tbName      string
	verses      []string
	initialized bool
}

type hymn struct {
	button int
	input  string
	tbName string
	verses []string
}

type verses struct {
	input      string
	tbName     string
	verses     []string
	verseIndex int
}

type speaker struct {
	button int
	input  string
	script string
	name   string
	tbName string
}

type activator struct {
	trigger   string
	input     string
	onAction  []string
	offAction []string
}

type camera struct {
	name     string
	IP       string
	user     string
	password string
	mode     string
}

type fader struct {
	fader int
	input string
}

type config struct {
	camera    map[string]*camera
	fader     map[int]*fader
	activator map[string]*map[string]activator
	prayer    map[int]*prayer
	pop       map[int]*pop
	shortcut  map[int]*shortcut
	response  map[int]*response
	hymn      map[int]*hymn
	speaker   map[int]*speaker
	initial   map[int]string
	mics      map[string]string
	misc      map[string]string
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
	nameToNumber     map[string]string
	numberToName     map[string]string
	overlayTBNames   map[string]string
}

type midiPorts struct {
	in  *midi.In
	out *midi.Out
}

type apcLEDS struct {
	buttons []int
	color   string
}

var currentVerses = new(verses)

// Translate from the APC midi mapping (0 is left button on last row
// to more logical numbering where 1 is the top-left button
var hButton = []int{
	57, 58, 59, 60, 61, 62, 63, 64, //Rectangular buttons
	49, 50, 51, 52, 53, 54, 55, 56,
	41, 42, 43, 44, 45, 46, 47, 48,
	33, 34, 35, 36, 37, 38, 39, 40,
	25, 26, 27, 28, 29, 30, 31, 32,
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
	//65, 66, 67, 68, 69, 70, 71, 72,
	64, 65, 66, 67, 68, 69, 70, 71,
	82, 83, 84, 85, 86, 87, 88, 89,
	99, 99, 99, 99, 99, 99, 99, 99,
	98,
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

var DEBUG *bool

func debug(msg ...interface{}) {
	if *DEBUG == true {
		fmt.Println(msg)
	}
}

func newState() state {
	var vmixState = new(state)
	vmixState.InputBusAAudio = make(map[int]bool)
	vmixState.InputBusBAudio = make(map[int]bool)
	vmixState.InputMasterAudio = make(map[int]bool)
	vmixState.InputPlaying = make(map[int]bool)
	vmixState.nameToNumber = make(map[string]string)
	vmixState.numberToName = make(map[string]string)
	vmixState.overlayTBNames = make(map[string]string)
	return *vmixState
}

//setInitialState will set the LEDs on the APC mini to their initial (default) state
func setInitialState(conf config, midiOutChan chan apcLEDS, vmixState state) {
	initState := conf.initial
	var redLeds apcLEDS
	var yellowLeds apcLEDS
	var greenLeds apcLEDS

	redLeds.color = "red"
	yellowLeds.color = "yellow"
	greenLeds.color = "green"

	for button, color := range initState {
		if color == "red" {
			redLeds.buttons = append(redLeds.buttons, button)
		}

		if color == "yellow" {
			yellowLeds.buttons = append(yellowLeds.buttons, button)
		}

		if color == "green" || color == "on" {
			greenLeds.buttons = append(greenLeds.buttons, button)
		}

	}

	midiOutChan <- redLeds
	midiOutChan <- yellowLeds
	midiOutChan <- greenLeds

	//Process activators based on current vmixState
	// Process current vmixState map to set LEDs on board with current state
	var vmixMessage string
	var inputS string

	// Active input
	inputS = strconv.Itoa(vmixState.Input)
	vmixMessage = "ACTS OK Input " + inputS + " 1"
	processActivator(vmixMessage, midiOutChan, conf)

	//Input has BusB assigned
	for input, active := range vmixState.InputBusBAudio {

		if active == true {
			inputS = strconv.Itoa(input)
			vmixMessage = "ACTS OK InputBusBAudio " + inputS + " 1"
			processActivator(vmixMessage, midiOutChan, conf)
		}
		if active == false {
			inputS = strconv.Itoa(input)
			vmixMessage = "ACTS OK InputBusBAudio " + inputS + " 0"
			processActivator(vmixMessage, midiOutChan, conf)
		}
	}
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
		input := inputs.SelectAttrValue("number", "")
		inputType := inputs.SelectAttrValue("type", "")
		state := inputs.SelectAttrValue("state", "")
		name := inputs.SelectAttrValue("title", "")

		vmixState.nameToNumber[name] = input
		vmixState.numberToName[input] = name

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
			vmixState.InputPlaying[number] = true
		}

		// Get the textbox name for title inputs
		if inputType == "GT" {
			// If there are multiple text boxes, select the first (index 0)
			textbox := inputs.SelectElement("text").SelectAttrValue("name", "")
			vmixState.overlayTBNames[name] = textbox
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
func newConfig(filename string, vmixState state) config {

	var scConfig = make(map[int]*shortcut)
	var respConfig = make(map[int]*response)
	var prayerConfig = make(map[int]*prayer)
	var popConfig = make(map[int]*pop)
	var hymnConfig = make(map[int]*hymn)
	var speakerConfig = make(map[int]*speaker)
	var activatorConfig = make(map[string]*map[string]activator)
	var faderConfig = make(map[int]*fader)
	var initialConfig = make(map[int]string)
	var micsConfig = make(map[string]string)
	var cameraConfig = make(map[string]*camera)

	conf := config{
		camera:    cameraConfig,
		fader:     faderConfig,
		activator: activatorConfig,
		prayer:    prayerConfig,
		pop:       popConfig,
		hymn:      hymnConfig,
		speaker:   speakerConfig,
		shortcut:  scConfig,
		response:  respConfig,
		initial:   initialConfig,
		mics:      micsConfig,
	}

	wb, err := excelize.OpenFile(filename)
	if err != nil {
		fmt.Println("Error opening workbook:", err)
		return conf
	}

	// NDI Cameras
	ndiRows, _ := wb.GetRows("NDI Cameras")

	for idx, row := range ndiRows {
		if idx != 0 && len(row) > 1 {
			ndiCam := new(camera)
			ndiCam.name = strings.ToLower(row[0])
			ndiCam.IP = row[1]
			ndiCam.user = row[2]
			ndiCam.password = row[3]
			ndiCam.mode = row[4]
			conf.camera[ndiCam.name] = ndiCam
		}
	}

	//Initial configuration of LED colors on APC mini
	inRows, _ := wb.GetRows("Initial State")
	for idx, row := range inRows {
		if idx != 0 && len(row) > 1 {
			if len(row[1]) > 0 {
				btn, _ := strconv.Atoi(row[0])
				initialConfig[btn] = row[1]
			}
		}
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
			var input string
			// If input is provided as a number translate it to a name
			if inputName, ok := vmixState.numberToName[row[1]]; ok {
				input = inputName
			} else {
				input = row[1]
			}

			or := new(response)
			or.button = btn
			or.input = input
			or.response = row[2]
			or.tbName = vmixState.overlayTBNames[input]
			conf.response[btn] = or
		}
	}

	// Prayers
	prayerCols, _ := wb.GetCols("Prayers")
	for _, col := range prayerCols {
		var pr = new(prayer)
		var input string

		// If input is provided as a number translate it to a name
		if inputName, ok := vmixState.numberToName[col[1]]; ok {
			input = inputName
		} else {
			input = col[1]
		}

		btn, _ := strconv.Atoi(col[2])
		pr.input = input
		pr.button = btn
		pr.tbName = vmixState.overlayTBNames[input]

		//verses start at col[3].  Get a sub slice
		verses := col[3:]
		pr.verses = verses
		conf.prayer[btn] = pr
	}

	// Prayers of the People
	// A separate section for prayers of the people as we need the overlay to be off
	// between responses
	popsCols, _ := wb.GetCols("PoP")
	for _, col := range popsCols {
		var response = new(pop)
		var input string

		// If input is provided as a number translate it to a name
		if inputName, ok := vmixState.numberToName[col[1]]; ok {
			input = inputName
		} else {
			input = col[1]
		}

		btn, _ := strconv.Atoi(col[2])
		response.input = input
		response.button = btn
		response.tbName = vmixState.overlayTBNames[input]
		response.initialized = false

		//responses start at col[3].  Get a sub slice
		verses := col[3:]
		response.verses = verses
		conf.pop[btn] = response
	}

	// Hymns
	hymnCols, _ := wb.GetCols("Hymns")
	for _, col := range hymnCols {
		var hy = new(hymn)
		var input string

		// If input is provided as a number translate it to a name
		if inputName, ok := vmixState.numberToName[col[1]]; ok {
			input = inputName
		} else {
			input = col[1]
		}
		btn, _ := strconv.Atoi(col[2])
		hy.input = input
		hy.button = btn
		hy.tbName = vmixState.overlayTBNames[input]

		//verses start at col[3].  Get a sub slice
		verses := col[3:]
		hy.verses = verses
		conf.hymn[btn] = hy
	}

	// Speakers
	spkRows, _ := wb.GetRows("Speakers")
	var input string
	for idx, row := range spkRows {
		if idx > 0 && len(row) > 0 {
			btn, _ := strconv.Atoi(row[1])
			// If input is provided as a number translate it to a name
			if inputName, ok := vmixState.numberToName[row[2]]; ok {
				input = inputName
			} else {
				input = row[2]
			}

			script := row[3]
			name := row[4]

			sp := speaker{
				button: btn,
				input:  input,
				script: script,
				name:   name,
				tbName: vmixState.overlayTBNames[input],
			}

			conf.speaker[btn] = &sp
		}
	}

	//Activators
	// map[trigger][input][vmixActivatorConfig]
	activatorCols, _ := wb.GetCols("Activators")

	for i, col := range activatorCols {
		if i > 0 && len(col) > 0 {
			var onActions []string
			var offActions []string
			var trigger string
			var input string

			//read the column in chunks of 3 lines, create a vmixActivatorConsole with the info, and
			//add to the inputMap for that trigger
			trigger = col[0]
			inputs := make(map[string]activator)
			for i := 1; col[i] != ""; i = i + 3 {
				input = col[i]

				// If the input is provided in the spreadsheet as a name we will need to get it's
				// input number, since the Activator Subscription in the API only returns numbers
				if inputNum, ok := vmixState.nameToNumber[input]; ok {
					input = inputNum

				}
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

	//Microphone assignments
	micCols, _ := wb.GetCols("microphones")

	// col[3] is the name, col[4] is the input
	names := micCols[3]
	inputs := micCols[4]

	for idx, name := range names {
		if idx > 0 {
			conf.mics[name] = inputs[idx]
		}
	}

	return conf
}

// watchConfigFile watches the configuration spreadsheet (responses.xlsx) for any changes (write).
// If any changes are detected it will reload the conf variable with the new data.
/* func watchConfigFile(conf *config, fileName string, vmixState state) {
	w := watcher.New()

	go func() {
		for {
			select {
			case event := <-w.Event:
				if event.Op.String() == "WRITE" {
					updateConfig(conf, fileName, vmixState)
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
func updateConfig(conf *config, fileName string, vmixState state) {
	wb, err := excelize.OpenFile(fileName)
	if err != nil {
		fmt.Println("Error opening workbook:", err)
	}

	//Initial configuration of LED colors on APC mini
	inRows, _ := wb.GetRows("Initial State")
	for idx, row := range inRows {
		if idx != 0 && len(row) > 1 {
			if len(row[1]) > 0 {
				btn, _ := strconv.Atoi(row[0])
				conf.initial[btn] = row[1]
			}
		}
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

			or := new(response)
			or.button = hButton[btn]
			or.input = row[1]
			or.response = row[2]
			conf.response[hButton[btn]] = or
		}
	}
	// Prayers
	prayerCols, _ := wb.GetCols("Prayers")
	for i, col := range prayerCols {
		if i != 0 && col != nil {
			var pr = new(prayer)
			var verses []string

			i := 3
			for len(col[i]) > i {
				verses = append(verses, col[i])
				i++
			}

			input := col[1]
			btn, _ := strconv.Atoi(col[2])
			pr.input = input
			pr.button = btn
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
			var input string

			trigger = col[0]
			inputs := make(map[string]activator)
			for i := 1; col[i] != ""; i = i + 3 {
				input = col[i]
				// If the input is provided in the spreadsheet as a name we will need to get it's
				// input number, since the Activator Subscription in the API only returns numbers
				if inputNum, ok := vmixState.nameToNumber[input]; ok {
					input = inputNum
				}
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
*/
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
	client.lock.Lock()
	pub := fmt.Sprintf("%v\r\n", message)
	_, err := client.w.WriteString(pub)
	if err != nil {
		panic(err)
	}
	_ = client.w.Flush()
	client.lock.Unlock()
	debug("Sent message to API:", message)
	return err
}

// getMessage connects to the vMix API and issues a subscription to activators.
// It then remains listening for any messages from the API server.  Any messages
// received are sent to the messageChan channel for consumption.  This is a blocking
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
	var input string
	var actions []string

	if len(messageSlice) == 5 {
		state = messageSlice[4]
		input = messageSlice[3]
	}

	if len(messageSlice) == 4 {
		state = messageSlice[3]
		input = "none"
	}

	if _, ok := conf.activator[trigger]; ok { //do we have an activator config for this trigger?
		debug("Processing activator for input", input)
		v := *conf.activator[trigger]
		if _, ok := v[input]; ok { //do we have an activator config for this trigger and input?
			if state == "0" {
				actions = v[input].offAction
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

// sendMidi is used to mimic an APC Mini.  It listens on port 2000 for button press commands.
// commands are: p [button number] -> press button
//               r [button number] -> release button
func sendMidi(midiInChan chan []byte) {

	//r := bufio.NewReader(os.Stdin)
	fmt.Println("Virtual MIDI Input")
	fmt.Println("---------------------")

	l, err := net.Listen("tcp", "localhost:2000")
	if err != nil {
		fmt.Println("Error listening:", err.Error())
		os.Exit(1)
	}

	defer func(l net.Listener) {
		err := l.Close()
		if err != nil {

		}
	}(l)

	c, err := l.Accept()
	if err != nil {
		fmt.Println(err)
		return
	}

	for {
		//fmt.Print("-> ")
		//textCommand, _ := r.ReadString('\n')

		netData, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			fmt.Println(err)
			return
		}
		if strings.TrimSpace(netData) == "STOP" {
			fmt.Println("Exiting TCP server!")
			return
		}

		// remove CRLF (\r\n) -- This works for Windows - need to modify for Linux
		cleanCommand := strings.TrimSpace(netData)
		sliceCommand := strings.Split(cleanCommand, " ")
		// Convert button number to integer
		intButton, _ := strconv.Atoi(sliceCommand[1])
		// Convert from my numbering of buttons to the APC numbering of buttons
		apcButton := oButton[intButton]
		// Convert button to byte
		byteButton := byte(apcButton)

		// message is a byte [type button velocity]
		// type 144, velocity 0 is a button up
		// type 144, velocity 127 is a button down
		// type 176 is a control change

		if sliceCommand[0] == "p" {
			message := []byte{144, byteButton, 127}
			midiInChan <- message
		}

		if sliceCommand[0] == "r" {
			message := []byte{144, byteButton, 0}
			midiInChan <- message
		}

	}
}

func processMidi(midiInChan chan []byte, midiOutChan chan apcLEDS, verseChan chan verses, client *vmixClient,
	conf config) {
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

				if _, ok := conf.response[button]; ok {
					execTextOverlay(client, button, conf)
					midiOutChan <- apcLEDS{
						buttons: []int{button},
						color:   "red",
					}
				}

				if item, ok := conf.prayer[button]; ok {
					tbName := conf.prayer[button].tbName
					*currentVerses = verses{item.input, tbName, item.verses, 0}
					verseChan <- *currentVerses
					//Turn on crowd mic
					//message = append(message, "FUNCTION AudioOn Input="+conf.mics["Crowd"])
					message = append(message, "FUNCTION AudioBusOn Value=M&Input="+conf.mics["Crowd"])
				}

				if item, ok := conf.pop[button]; ok {

					if conf.pop[button].initialized == false {
						tbName := conf.pop[button].tbName
						*currentVerses = verses{item.input, tbName, item.verses, 0}
						verseChan <- *currentVerses
						conf.pop[button].initialized = true
					} else {
						if currentVerses.input != "" {
							currentVerses.verseIndex++
							if currentVerses.verseIndex < len(currentVerses.verses) {
								verseChan <- *currentVerses
								midiOutChan <- apcLEDS{
									buttons: []int{button},
									color:   "red",
								}
							} else {
								//we got to the end. Turn off the button led
								midiOutChan <- apcLEDS{
									buttons: []int{button},
									color:   "off",
								}
							}

						}
					}
					//Turn on crowd mic
					message = append(message, "FUNCTION AudioBusOn Value=M&Input="+conf.mics["Crowd"])
				}

				if item, ok := conf.hymn[button]; ok {
					tbName := conf.hymn[button].tbName
					*currentVerses = verses{item.input, tbName, item.verses, 0}
					verseChan <- *currentVerses
				}

				if speaker, ok := conf.speaker[button]; ok {
					var m string

					name := speaker.name
					script := speaker.script
					input := speaker.input
					textBox := speaker.tbName

					if len(script) > 1 {
						m = "FUNCTION ScriptStart Value=" +
							url.QueryEscape(script)
						_ = SendMessage(client, m)
						//Give the script some time to complete
						time.Sleep(time.Millisecond * 500)
					}

					m = "FUNCTION SetText Input=" + input + "&SelectedName=" +
						url.QueryEscape(textBox) +
						"&Value=" + url.QueryEscape(name)
					message = append(message, m)
					time.Sleep(time.Millisecond * 1200)
					m = "FUNCTION OverlayInput1In Input=" + input
					message = append(message, m)

				}

				if _, ok := conf.shortcut[button]; ok {

					for _, action := range conf.shortcut[button].actionsPressed {
						debug("Performing action:", action)
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

						} else if strings.HasPrefix(action, "preset") {

							// Move PTZ camera to preset position
							// syntax: preset camera_name preset_number
							parts := strings.Split(action, " ")
							camera := strings.ToLower(parts[1])
							preset := parts[2]
							debug("Starting move camera '" + camera + "' to preset: " + preset)

							if cameraConfig, ok := conf.camera[camera]; ok {
								cameraPreset(cameraConfig, preset)
							}

						} else if action == "Next" {
							if currentVerses.input != "" {
								currentVerses.verseIndex++
								if currentVerses.verseIndex < len(currentVerses.verses) {
									verseChan <- *currentVerses
								}
								midiOutChan <- apcLEDS{
									buttons: []int{button},
									color:   "yellow",
								}
							}
						} else if action == "Prev" {
							if currentVerses.input != "" {
								currentVerses.verseIndex--
								if currentVerses.verseIndex >= 0 {
									verseChan <- *currentVerses
								}
							}
						} else if action == "OvOff" {
							m := "FUNCTION OverlayInput1Out Input=" + currentVerses.input
							*currentVerses = verses{"", "", []string{}, 0}

							message = append(message, m)
							// Run OverlayOff script
							message = append(message, "FUNCTION ScriptStart Value=OverlayOff")

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

				//PoP remove response overlay
				if _, ok := conf.pop[button]; ok {
					message = append(message, "FUNCTION OverlayInput1Out")
					//Turn off crowd mic
					//message = append(message, "FUNCTION AudioOff Input="+conf.mics["Crowd"])
					message = append(message, "FUNCTION AudioBusOff Value=M&Input="+conf.mics["Crowd"])
				}

				//Check respConfig to see if we have a match. If so remove the overlay and turn
				//off the crowd mic

				if _, ok := conf.response[button]; ok {
					message = append(message, "FUNCTION OverlayInput1Out")
					//message = append(message, "FUNCTION AudioOff Input="+conf.mics["Crowd"])
					message = append(message, "FUNCTION AudioBusOff Value=M&Input="+conf.mics["Crowd"])

					if button == 14 || button == 15 || button == 16 {
						midiOutChan <- apcLEDS{
							buttons: []int{button},
							color:   "yellow",
						}
					} else {
						midiOutChan <- apcLEDS{
							buttons: []int{button},
							color:   "off",
						}
					}
				}

				if _, ok := conf.shortcut[button]; ok {
					for _, action := range conf.shortcut[button].actionsReleased {
						if action != "" {
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
					if input == "Dynamic1" {
						input = "Dynamic1"
						m = "FUNCTION SetVolume Input=" + input + "&Value=" + volumeS
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

func cameraPreset(cameraConfig *camera, preset string) {

	if strings.ToLower(cameraConfig.mode) == "null" {
		debug("Null camera: not doing anything")
		return
	}
	if strings.ToLower(cameraConfig.mode) == "onvif" {
		dev, err := onvif.NewDevice(cameraConfig.IP)
		if err != nil {
			debug("Unable to connect to NDI camera: ", cameraConfig.name, err)
		}

		dev.Authenticate(cameraConfig.user, cameraConfig.password)

		if strings.ToLower(preset) == "home" {
			_, err := dev.CallMethod(ptz.GotoHomePosition{})
			if err != nil {
				debug("NDI camera error moving to preset ", preset, err)
			}
		} else {
			_, err := dev.CallMethod(ptz.GotoPreset{
				PresetToken: onvif2.ReferenceToken(preset)})
			if err != nil {
				debug("NDI camera error moving to preset ", preset, err)
			}
		}

		debug("Camera ", cameraConfig.name, "moved to preset", preset)
	}

	if strings.ToLower(cameraConfig.mode) == "cgi" {
		// for bzbgear ptz cameras
		camURL := "http://" + cameraConfig.IP + "/cgi-bin/ptzctrl.cgi?ptzcmd&poscall&" + preset
		fmt.Println(camURL)
		//camURL := "http://10.0.20.10/cgi-bin/ptzctrl.cgi?ptzcmd&poscall&1"
		resp, err := http.Get(camURL)
		if err != nil {
			print(err)
		}

		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			fmt.Println("HTTP Status is in the 2xx range")
		} else {
			fmt.Println("Argh! Broken")
		}
	}

	if strings.ToLower(cameraConfig.mode) == "visca" {
		iPreset, _ := strconv.Atoi(preset)
		bPreset := byte(iPreset)
		s := []byte{0x81, 0x01, 0x04, 0x3F, 0x02, bPreset, 0xFF}

		viscaCon, _ := net.Dial("udp", cameraConfig.IP)

		defer func(viscaCon net.Conn) {
			err := viscaCon.Close()
			if err != nil {
				debug("Unable to connect via visca to camera '" + cameraConfig.name + "' at " + cameraConfig.IP)
			}
		}(viscaCon)

		_, err := viscaCon.Write(s)
		if err != nil {
			debug("Unable to write via visca to camera '" + cameraConfig.name + "' at " + cameraConfig.IP)
		}
	}

}

func execTextOverlay(client *vmixClient, button int, conf config) {
	var message string

	if item, ok := conf.response[button]; ok {

		//set the text
		message = "FUNCTION SetText Input=" + url.QueryEscape(item.input) + "&SelectedName=" +
			url.QueryEscape(item.tbName) +
			"&Value=" + url.QueryEscape(item.response)
		_ = SendMessage(client, message)

		// Turn on the crowd mic
		//message = "FUNCTION AudioOn Input=" + conf.mics["Crowd"]
		message = "FUNCTION AudioBusOn Value=M&Input=" + conf.mics["Crowd"]
		_ = SendMessage(client, message)

		//pause for 100 milliseconds to allow text to update in the title
		d, _ := time.ParseDuration("100ms")
		time.Sleep(d)
		message = "FUNCTION OverlayInput1In Input=" + item.input
		_ = SendMessage(client, message)
	}
}

func versePager(verseChan chan verses, client *vmixClient) {
	var message string
	for {

		item := <-verseChan
		debug("versePager received item:", item)

		message = "FUNCTION SetText Input=" + url.QueryEscape(item.input) + "&SelectedName=TextBlock1.Text&Value=" +
			url.QueryEscape(item.verses[currentVerses.verseIndex])
		err := SendMessage(client, message)
		if err != nil {
			fmt.Print("***Error sending message: ", err)
		}
		// Wait a bit to ensure title text is changed
		time.Sleep(time.Millisecond * 300)
		message = "FUNCTION OverlayInput1In Input=" + item.input
		_ = SendMessage(client, message)
	}
}

func setAllLed(color string, midiOutChan chan apcLEDS) {
	var btn []int

	for a := 1; a < 81; a++ {
		btn = append(btn, a)
	}
	leds := apcLEDS{
		buttons: btn,
		color:   color}
	midiOutChan <- leds

}

func getMIDIPorts() (err error, midiPort midiPorts) {
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
		err = nil
	} else {
		err = errors.New("unable to find an APC Mini")
	}

	return err, midiPort
}

func initMidi(midiInChan chan []byte, midiOutChan chan apcLEDS) {
	var midiPort = new(midiPorts)
	var err error

	err, *midiPort = getMIDIPorts()
	if err != nil {
		panic(err)
	}

	rd := reader.New(
		reader.NoLogger(),

		// Fetch every message
		reader.Each(func(pos *reader.Position, msg midi.Message) {
			midiInChan <- msg.Raw()
		}),
	)

	err = rd.ListenTo(*midiPort.in)
	if err != nil {
		fmt.Println(err)
		return
	}

	go watchdog(midiPort, midiInChan, midiOutChan)

	for {
		apcLED := <-midiOutChan
		setAPCLED(apcLED, midiPort.out)
	}
}

func setAPCLED(led apcLEDS, outPort *midi.Out) {

	debug("Received apcLED", led)
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
		if led.color == "off" {
			_ = writer.NoteOff(wr, uint8(b))
		} else {
			_ = writer.NoteOn(wr, uint8(b), values[led.color])
		}
	}
}

func watchdog(midiPort *midiPorts, midiInChan chan []byte, midiOutChan chan apcLEDS) {
	for {
		wr := writer.New(*midiPort.out)
		wr.ConsolidateNotes(false)
		err := writer.NoteOff(wr, 100)
		if err != nil {
			// Attempt to re-connect to APC Mini
			for err != nil {
				debug("Attempting to re-connect to APC")
				err, *midiPort = getMIDIPorts()
				time.Sleep(time.Second * 2)
			}
			// Close current ports
			in := *midiPort.in
			out := *midiPort.out
			in.Close()
			out.Close()

			go initMidi(midiInChan, midiOutChan)
			return
		}
		time.Sleep(time.Second * 2)
	}
}

// killOthers will kill other vmixAPC processes running.  This allows for setting up a browser input in vmix to
// run the apcVmix program.  If vmixAPC starts acting up, just refresh the browser and a new instance will
// come up.
func killOthers() {
	processes, _ := ps.Processes()
	selfPID := os.Getpid()
	fmt.Println("Self PID: ", selfPID)
	for _, process := range processes {
		var pid string
		if process.Executable() == "vmixAPC.exe" && process.Pid() != selfPID {
			pid = strconv.Itoa(process.Pid())
			kill := exec.Command("TASKKILL", "/T", "/F", "/PID", pid)
			err := kill.Run()
			if err != nil {
				fmt.Println("Error killing vmixAPC process", err)
			}
			fmt.Println("Killed ", process.Pid())
		}
	}
}

func main() {

	//Process any CLI args
	DEBUG = flag.Bool("debug", false, "Display debugging info on stdout (true/false)")
	apiAddress := flag.String("apiAddr", "127.0.0.1:8099", "IP address and port of vMix API (127.0.0.1:8099)")
	fileName := flag.String("fileName", "D:/OneDrive/Episcopal Church of Reconciliation/Livestream - Documents/Livestream.xlsm",
		"Path and filename to the vmixAPC configuration workbook")
	flag.Parse()
	debug("Starting vMixAPC ...")

	killOthers()

	var midiInChan = make(chan []byte, 10)
	var midiOutChan = make(chan apcLEDS, 40)
	var messageChan = make(chan string)
	var verseChan = make(chan verses)
	var wg sync.WaitGroup

	vcConf := vcConfig{
		apiAddress:  *apiAddress,
		messageChan: messageChan,
		wg:          &wg,
	}

	vmixState := updateVmixState(vcConf)
	vmConfig := newConfig(*fileName, vmixState)

	setAllLed("off", midiOutChan)

	vmClient, _ := vmixAPIConnect(vcConf)

	//	go watchConfigFile(&vmConfig, *fileName, vmixState)

	go getMessage(vmClient)
	go processVmixMessage(vmClient, midiOutChan, vmixState, vmConfig)

	go initMidi(midiInChan, midiOutChan)
	go processMidi(midiInChan, midiOutChan, verseChan, vmClient, vmConfig)
	go versePager(verseChan, vmClient)

	setInitialState(vmConfig, midiOutChan, vmixState)

	go sendMidi(midiInChan)

	defer vmClient.conn.Close()
	defer close(vmClient.messageChan)
	defer close(midiInChan)

	wg.Add(2)
	wg.Wait()
}
