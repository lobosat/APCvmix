package main

import (
	"bufio"
	"fmt"
	"gitlab.com/gomidi/midi"
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

func ProcessMidi(midiMessage chan []byte) {
	for {
		msg := <-midiMessage
		switch msg[0] {
		case 144:
			fmt.Println("Button Down:", msg[1])
		case 128:
			fmt.Println("Button Up:", msg[1])
		case 176:
			fmt.Println("Control change. Fader:", msg[1], "Value:", msg[2])
		}
	}
}

func ListenMidi(drv midi.Driver, midiMessage chan []byte, wg *sync.WaitGroup) {
	//Listen to midi port, push any messages to the midiMessage channel
	defer drv.Close()
	in, err := midi.OpenIn(drv, 1, "")

	if err != nil {
		wg.Done()
		fmt.Println("Error in ListenMidi", err)
	}
	for {
		_ = in.SetListener(func(data []byte, deltaMicroseconds int64) {
			midiMessage <- data
		})
	}
}

func main() {
	var wg sync.WaitGroup
	wg.Add(2)

	//vmixClient := NewClient()
	//err := vmixClient.Connect("192.168.1.173:8099")
	//vmixMessage := make(chan string)
	//defer close(vmixMessage)
	//

	//
	//go vmixClient.GetMessage(vmixMessage, &wg)
	//go ProcessMessage(vmixMessage)

	midiMessage := make(chan []byte, 100)
	defer close(midiMessage)

	drv, err := rtmididrv.New()
	if err != nil {
		panic(err)
	}
	defer drv.Close()

	go ListenMidi(drv, midiMessage, &wg)
	go ProcessMidi(midiMessage)

	wg.Wait()

}
